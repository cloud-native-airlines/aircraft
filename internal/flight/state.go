package flight

import "time"

// Status is the lifecycle value ADS-B stores.
type Status string

const (
	StatusParked Status = "parked"
	StatusFlying Status = "flying"
)

// Phase is the finer movement detail (for logs and state inspection).
type Phase string

const (
	PhaseParked  Phase = "parked"
	PhaseClimb   Phase = "climb"
	PhaseCruise  Phase = "cruise"
	PhaseDescent Phase = "descent"
	PhaseArrived Phase = "arrived"
)

// State is the aircraft's physical state at a simulated time. Field units match
// the ADS-B report contract: altitude in meters, ground speed in m/s, heading in
// degrees [0, 360).
type State struct {
	SimulatedAt time.Time
	Position    Point
	AltitudeM   float64
	GroundMPS   float64
	HeadingDeg  float64
	Status      Status
	Phase       Phase
}

// Terminal reports whether this state is at or after arrival (the flight has
// landed), which is the signal for the process to record it and exit.
func (s State) Terminal() bool { return s.Phase == PhaseArrived }

// Calculate returns the physical state for a plan at a simulated time. It is a
// pure function: the same plan and time always produce the same state, so
// repeated or missed ticks never move the aircraft incorrectly.
func Calculate(p Plan, at time.Time) State {
	prof := p.profile()
	// Heading holds the route's initial bearing; while stationary this is the
	// documented value, and over this leg the bearing barely changes.
	heading := BearingDeg(p.Origin, p.Destination)

	// Before departure: parked at the origin.
	if !at.After(p.Departure) {
		return State{
			SimulatedAt: at, Position: p.Origin,
			AltitudeM: 0, GroundMPS: 0, HeadingDeg: heading,
			Status: StatusParked, Phase: PhaseParked,
		}
	}

	// At or after arrival: parked at the destination.
	if !at.Before(p.Arrival) {
		return State{
			SimulatedAt: at, Position: p.Destination,
			AltitudeM: 0, GroundMPS: 0, HeadingDeg: heading,
			Status: StatusParked, Phase: PhaseArrived,
		}
	}

	// In flight: t seconds since departure.
	t := at.Sub(p.Departure).Seconds()
	dist, speed := prof.alongTrack(t)
	alt, phase := prof.altitudeAndPhase(t, p.CruiseAltitudeM)

	pos := p.Origin
	if prof.distM > 0 {
		pos = Interpolate(p.Origin, p.Destination, dist/prof.distM)
	}
	// Instantaneous bearing from the current position toward the destination.
	heading = BearingDeg(pos, p.Destination)

	return State{
		SimulatedAt: at, Position: pos,
		AltitudeM: alt, GroundMPS: speed, HeadingDeg: heading,
		Status: StatusFlying, Phase: phase,
	}
}

// alongTrack returns the distance covered (meters) and the instantaneous ground
// speed (m/s) at t seconds into the flight, integrating the trapezoidal speed
// profile.
func (pr profile) alongTrack(t float64) (dist, speed float64) {
	tc, tcr, td, vmax := pr.climbS, pr.cruiseS, pr.descentS, pr.vmaxMPS
	switch {
	case t <= tc && tc > 0: // climb: linear ramp 0 -> vmax
		speed = vmax * (t / tc)
		dist = vmax * t * t / (2 * tc)
	case t <= tc+tcr: // cruise: constant vmax
		speed = vmax
		dist = vmax*tc/2 + vmax*(t-tc)
	default: // descent: linear ramp vmax -> 0
		td0 := t - (pr.totalS - td) // seconds into descent
		if td > 0 {
			speed = vmax * (1 - td0/td)
			dist = pr.distM - vmax*(td-td0)*(td-td0)/(2*td)
		} else {
			speed = vmax
			dist = pr.distM
		}
	}
	return dist, speed
}

// altitudeAndPhase returns the altitude (meters) and movement phase at t seconds
// into the flight, using a linear-in-time trapezoid for altitude.
func (pr profile) altitudeAndPhase(t, cruiseAlt float64) (float64, Phase) {
	tc, tcr, td := pr.climbS, pr.cruiseS, pr.descentS
	switch {
	case t <= tc && tc > 0:
		return cruiseAlt * (t / tc), PhaseClimb
	case t <= tc+tcr:
		return cruiseAlt, PhaseCruise
	default:
		td0 := t - (pr.totalS - td)
		if td > 0 {
			return cruiseAlt * (1 - td0/td), PhaseDescent
		}
		return cruiseAlt, PhaseCruise
	}
}
