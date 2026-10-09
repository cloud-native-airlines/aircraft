package flight

import (
	"fmt"
	"time"
)

// Plan is a single flight leg. Times are in the simulated world (UTC).
type Plan struct {
	FlightID   string
	AircraftID string

	Origin      Point
	Destination Point

	Departure time.Time
	Arrival   time.Time

	CruiseAltitudeM float64
	ClimbFraction   float64 // share of total flight time spent climbing
	DescentFraction float64 // share of total flight time spent descending
}

// Validate checks the plan is internally consistent.
func (p Plan) Validate() error {
	if p.FlightID == "" {
		return fmt.Errorf("flight_id is required")
	}
	if p.AircraftID == "" {
		return fmt.Errorf("aircraft_id is required")
	}
	if !p.Arrival.After(p.Departure) {
		return fmt.Errorf("arrival (%s) must be after departure (%s)", p.Arrival, p.Departure)
	}
	if p.CruiseAltitudeM < 0 {
		return fmt.Errorf("cruise_altitude_m must not be negative")
	}
	if p.ClimbFraction < 0 || p.DescentFraction < 0 {
		return fmt.Errorf("climb and descent fractions must not be negative")
	}
	if p.ClimbFraction+p.DescentFraction > 1 {
		return fmt.Errorf("climb_fraction + descent_fraction must not exceed 1 (got %.2f)",
			p.ClimbFraction+p.DescentFraction)
	}
	return nil
}

// totalSeconds is the flight duration in simulated seconds.
func (p Plan) totalSeconds() float64 { return p.Arrival.Sub(p.Departure).Seconds() }

// profile holds the derived trapezoidal parameters for a plan.
type profile struct {
	totalS   float64
	climbS   float64
	cruiseS  float64
	descentS float64
	distM    float64
	vmaxMPS  float64 // cruise ground speed derived so distance integrates correctly
}

func (p Plan) profile() profile {
	total := p.totalSeconds()
	climb := total * p.ClimbFraction
	descent := total * p.DescentFraction
	cruise := total - climb - descent
	dist := DistanceM(p.Origin, p.Destination)

	// Area of the speed trapezoid must equal distance:
	//   dist = vmax*(climb/2 + cruise + descent/2)
	denom := climb/2 + cruise + descent/2
	var vmax float64
	if denom > 0 {
		vmax = dist / denom
	}
	return profile{
		totalS:   total,
		climbS:   climb,
		cruiseS:  cruise,
		descentS: descent,
		distM:    dist,
		vmaxMPS:  vmax,
	}
}
