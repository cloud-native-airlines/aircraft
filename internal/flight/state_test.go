package flight

import (
	"math"
	"testing"
	"time"
)

var (
	msp = Point{Lat: 44.8848, Lon: -93.2223}
	ord = Point{Lat: 41.9742, Lon: -87.9073}
	dep = time.Date(2026, 10, 6, 14, 0, 0, 0, time.UTC)
	arr = time.Date(2026, 10, 6, 14, 12, 0, 0, time.UTC)
)

func testPlan() Plan {
	return Plan{
		FlightID: "CNA100", AircraftID: "N100CA",
		Origin: msp, Destination: ord,
		Departure: dep, Arrival: arr,
		CruiseAltitudeM: 10668,
		ClimbFraction:   0.25, DescentFraction: 0.25,
	}
}

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func at(min, sec int) time.Time {
	return dep.Add(time.Duration(min)*time.Minute + time.Duration(sec)*time.Second)
}

func TestParkedBeforeDeparture(t *testing.T) {
	s := Calculate(testPlan(), dep.Add(-time.Minute))
	if s.Status != StatusParked || s.Phase != PhaseParked {
		t.Fatalf("want parked/parked, got %s/%s", s.Status, s.Phase)
	}
	if s.AltitudeM != 0 || s.GroundMPS != 0 {
		t.Fatalf("want zero alt/speed, got %.1f/%.1f", s.AltitudeM, s.GroundMPS)
	}
	if !approx(s.Position.Lat, msp.Lat, 1e-9) || !approx(s.Position.Lon, msp.Lon, 1e-9) {
		t.Fatalf("want origin, got %+v", s.Position)
	}
}

func TestParkedAtAndAfterArrival(t *testing.T) {
	for _, when := range []time.Time{arr, arr.Add(time.Hour)} {
		s := Calculate(testPlan(), when)
		if s.Status != StatusParked || s.Phase != PhaseArrived {
			t.Fatalf("at %s: want parked/arrived, got %s/%s", when, s.Status, s.Phase)
		}
		if s.AltitudeM != 0 || s.GroundMPS != 0 {
			t.Fatalf("at %s: want zero alt/speed, got %.1f/%.1f", when, s.AltitudeM, s.GroundMPS)
		}
		if !approx(s.Position.Lat, ord.Lat, 1e-9) || !approx(s.Position.Lon, ord.Lon, 1e-9) {
			t.Fatalf("at %s: want destination, got %+v", when, s.Position)
		}
		if !s.Terminal() {
			t.Fatalf("at %s: expected Terminal()", when)
		}
	}
}

func TestPhaseBoundaries(t *testing.T) {
	p := testPlan()
	cases := []struct {
		name  string
		when  time.Time
		phase Phase
	}{
		{"climb midpoint", at(1, 30), PhaseClimb},      // 1.5 min into a 3 min climb
		{"cruise", at(6, 0), PhaseCruise},              // middle of cruise
		{"descent midpoint", at(10, 30), PhaseDescent}, // 1.5 min into the final 3 min
	}
	for _, c := range cases {
		s := Calculate(p, c.when)
		if s.Phase != c.phase {
			t.Errorf("%s: want phase %s, got %s", c.name, c.phase, s.Phase)
		}
		if s.Status != StatusFlying {
			t.Errorf("%s: want flying, got %s", c.name, s.Status)
		}
	}
}

func TestClimbMidpointRampsHalfway(t *testing.T) {
	p := testPlan()
	prof := p.profile()
	s := Calculate(p, at(1, 30)) // halfway through the climb
	if !approx(s.AltitudeM, p.CruiseAltitudeM/2, 1.0) {
		t.Errorf("climb midpoint altitude: want ~%.0f, got %.1f", p.CruiseAltitudeM/2, s.AltitudeM)
	}
	if !approx(s.GroundMPS, prof.vmaxMPS/2, 0.5) {
		t.Errorf("climb midpoint speed: want ~%.1f, got %.1f", prof.vmaxMPS/2, s.GroundMPS)
	}
}

func TestCruiseAtMaxSpeedAndAltitude(t *testing.T) {
	p := testPlan()
	prof := p.profile()
	s := Calculate(p, at(6, 0))
	if !approx(s.AltitudeM, p.CruiseAltitudeM, 1e-6) {
		t.Errorf("cruise altitude: want %.0f, got %.1f", p.CruiseAltitudeM, s.AltitudeM)
	}
	if !approx(s.GroundMPS, prof.vmaxMPS, 1e-6) {
		t.Errorf("cruise speed: want %.1f, got %.1f", prof.vmaxMPS, s.GroundMPS)
	}
}

// The integral of the speed profile over the whole flight must equal the
// great-circle distance — that is what makes reported speed describe the
// actual movement.
func TestSpeedIntegralEqualsDistance(t *testing.T) {
	prof := testPlan().profile()
	dist, _ := prof.alongTrack(prof.totalS)
	if !approx(dist, prof.distM, 1.0) {
		t.Fatalf("along-track at end: want %.1f m, got %.1f m", prof.distM, dist)
	}
	// Numerically integrate speed and compare.
	const steps = 100000
	dt := prof.totalS / steps
	var sum float64
	for i := 0; i < steps; i++ {
		_, v := prof.alongTrack((float64(i) + 0.5) * dt)
		sum += v * dt
	}
	if !approx(sum, prof.distM, prof.distM*1e-3) {
		t.Fatalf("integrated speed: want ~%.0f m, got %.0f m", prof.distM, sum)
	}
}

func TestHeadingSoutheast(t *testing.T) {
	// MSP -> ORD is roughly southeast; bearing should be ~125 degrees, well away
	// from the arbitrary 264 in the sample fixtures.
	s := Calculate(testPlan(), at(6, 0))
	if s.HeadingDeg < 110 || s.HeadingDeg > 140 {
		t.Errorf("heading: want ~125 (SE), got %.1f", s.HeadingDeg)
	}
}

func TestDeterministic(t *testing.T) {
	p := testPlan()
	when := at(4, 37)
	a := Calculate(p, when)
	b := Calculate(p, when)
	if a != b {
		t.Fatalf("same inputs produced different states:\n a=%+v\n b=%+v", a, b)
	}
}

func TestProgressesAlongRoute(t *testing.T) {
	p := testPlan()
	// Position should advance monotonically away from origin toward destination.
	prev := DistanceM(msp, Calculate(p, dep).Position)
	for m := 1; m <= 12; m++ {
		d := DistanceM(msp, Calculate(p, at(m, 0)).Position)
		if d < prev-1 {
			t.Fatalf("distance from origin went backward at minute %d: %.0f < %.0f", m, d, prev)
		}
		prev = d
	}
}
