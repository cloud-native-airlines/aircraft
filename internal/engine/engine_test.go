package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/cloud-native-airlines/flight/internal/flight"
)

var (
	dep = time.Date(2026, 10, 6, 14, 0, 0, 0, time.UTC)
	arr = time.Date(2026, 10, 6, 14, 12, 0, 0, time.UTC)
)

func testPlan() flight.Plan {
	return flight.Plan{
		FlightID: "CNA100", AircraftID: "N100CA",
		Origin:      flight.Point{Lat: 44.8848, Lon: -93.2223},
		Destination: flight.Point{Lat: 41.9742, Lon: -87.9073},
		Departure:   dep, Arrival: arr,
		CruiseAltitudeM: 10668, ClimbFraction: 0.25, DescentFraction: 0.25,
	}
}

func tick(run string, id int64, min int) Tick {
	return Tick{RunID: run, TickID: id, SimulatedAt: dep.Add(time.Duration(min) * time.Minute)}
}

func TestFirstTickBindsRun(t *testing.T) {
	e := New(testPlan(), "")
	if e.RunID() != "" {
		t.Fatalf("expected unbound run initially")
	}
	r, err := e.Apply(tick("run-A", 0, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.Applied {
		t.Fatalf("first tick should be applied")
	}
	if e.RunID() != "run-A" {
		t.Fatalf("expected bound to run-A, got %q", e.RunID())
	}
}

func TestConfiguredRunBinding(t *testing.T) {
	e := New(testPlan(), "run-cfg")
	if _, err := e.Apply(tick("run-other", 0, 0)); !errors.Is(err, ErrWrongRun) {
		t.Fatalf("want ErrWrongRun, got %v", err)
	}
	if _, err := e.Apply(tick("run-cfg", 0, 0)); err != nil {
		t.Fatalf("configured run should be accepted: %v", err)
	}
}

func TestNewerTickAdvances(t *testing.T) {
	e := New(testPlan(), "")
	e.Apply(tick("r", 3, 3))
	r, _ := e.Apply(tick("r", 6, 6))
	if !r.Applied {
		t.Fatalf("newer tick should be applied")
	}
	cur, ok := e.Current()
	if !ok || cur.Phase != flight.PhaseCruise {
		t.Fatalf("expected cruise at minute 6, got %+v", cur)
	}
}

func TestOlderTickDoesNotRewind(t *testing.T) {
	e := New(testPlan(), "")
	e.Apply(tick("r", 6, 6)) // cruise
	before, _ := e.Current()

	r, err := e.Apply(tick("r", 2, 2)) // older: still climbing
	if err != nil {
		t.Fatalf("older tick should not error: %v", err)
	}
	if r.Applied {
		t.Fatalf("older tick must not be applied")
	}
	if r.State.Phase != flight.PhaseClimb {
		t.Fatalf("older tick should still compute its own state (climb), got %s", r.State.Phase)
	}
	after, _ := e.Current()
	if after != before {
		t.Fatalf("current state was rewound by an older tick")
	}
}

func TestDuplicateTickIsIdempotent(t *testing.T) {
	e := New(testPlan(), "")
	first, _ := e.Apply(tick("r", 6, 6))
	dup, err := e.Apply(tick("r", 6, 6))
	if err != nil {
		t.Fatalf("duplicate should not error: %v", err)
	}
	if dup.Applied {
		t.Fatalf("duplicate should not re-apply")
	}
	if dup.State != first.State {
		t.Fatalf("duplicate should recompute the same state")
	}
}

func TestConflictingContentRejected(t *testing.T) {
	e := New(testPlan(), "")
	e.Apply(tick("r", 6, 6))
	conflict := Tick{RunID: "r", TickID: 6, SimulatedAt: dep.Add(7 * time.Minute)}
	if _, err := e.Apply(conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestWrongRunRejectedAfterBinding(t *testing.T) {
	e := New(testPlan(), "")
	e.Apply(tick("run-A", 0, 0))
	if _, err := e.Apply(tick("run-B", 1, 1)); !errors.Is(err, ErrWrongRun) {
		t.Fatalf("want ErrWrongRun for a delayed other-run tick, got %v", err)
	}
}

func TestTerminalOnArrival(t *testing.T) {
	e := New(testPlan(), "")
	e.Apply(tick("r", 6, 6))
	r, _ := e.Apply(tick("r", 12, 12)) // arrival
	if !r.Applied || !r.Terminal {
		t.Fatalf("arrival tick should be applied and terminal, got applied=%v terminal=%v", r.Applied, r.Terminal)
	}
	if r.State.Status != flight.StatusParked || r.State.Phase != flight.PhaseArrived {
		t.Fatalf("arrival state should be parked/arrived, got %s/%s", r.State.Status, r.State.Phase)
	}
}

func TestColdStartAfterArrivalIsTerminal(t *testing.T) {
	// A replacement pod that starts after arrival computes arrived state and exits.
	e := New(testPlan(), "")
	r, err := e.Apply(Tick{RunID: "r", TickID: 99, SimulatedAt: arr.Add(time.Hour)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.Applied || !r.Terminal {
		t.Fatalf("post-arrival cold start should be applied and terminal")
	}
}
