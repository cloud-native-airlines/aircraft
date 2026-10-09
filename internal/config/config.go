// Package config loads the flight's identity, plan, and runtime settings from a
// plan file and environment variables.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/cloud-native-airlines/flight/internal/flight"
)

// Config is the resolved startup configuration.
type Config struct {
	Plan       flight.Plan
	RunID      string // optional; empty means bind to the first tick
	ListenAddr string
}

// airport mirrors the origin/destination shape of the shared scenario files.
type airport struct {
	IATA         string  `json:"iata"`
	LatitudeDeg  float64 `json:"latitude_deg"`
	LongitudeDeg float64 `json:"longitude_deg"`
}

type planFile struct {
	FlightID        string    `json:"flight_id"`
	AircraftID      string    `json:"aircraft_id"`
	Origin          airport   `json:"origin"`
	Destination     airport   `json:"destination"`
	DepartureTime   time.Time `json:"departure_time"`
	ArrivalTime     time.Time `json:"arrival_time"`
	CruiseAltitudeM *float64  `json:"cruise_altitude_m,omitempty"`
	ClimbFraction   *float64  `json:"climb_fraction,omitempty"`
	DescentFraction *float64  `json:"descent_fraction,omitempty"`
}

// Load resolves configuration from the environment. FLIGHT_PLAN (a path to a
// plan JSON) is required.
func Load() (Config, error) {
	path := os.Getenv("FLIGHT_PLAN")
	if path == "" {
		return Config{}, fmt.Errorf("FLIGHT_PLAN is required (path to a flight-plan JSON)")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading FLIGHT_PLAN: %w", err)
	}
	var pf planFile
	if err := json.Unmarshal(raw, &pf); err != nil {
		return Config{}, fmt.Errorf("parsing FLIGHT_PLAN: %w", err)
	}

	plan := flight.Plan{
		FlightID:    pf.FlightID,
		AircraftID:  pf.AircraftID,
		Origin:      flight.Point{Lat: pf.Origin.LatitudeDeg, Lon: pf.Origin.LongitudeDeg},
		Destination: flight.Point{Lat: pf.Destination.LatitudeDeg, Lon: pf.Destination.LongitudeDeg},
		Departure:   pf.DepartureTime.UTC(),
		Arrival:     pf.ArrivalTime.UTC(),
		// Defaults; overridden by the plan file, then the environment below.
		CruiseAltitudeM: 10668,
		ClimbFraction:   0.25,
		DescentFraction: 0.25,
	}
	if pf.CruiseAltitudeM != nil {
		plan.CruiseAltitudeM = *pf.CruiseAltitudeM
	}
	if pf.ClimbFraction != nil {
		plan.ClimbFraction = *pf.ClimbFraction
	}
	if pf.DescentFraction != nil {
		plan.DescentFraction = *pf.DescentFraction
	}

	if plan.CruiseAltitudeM, err = envFloat("FLIGHT_CRUISE_ALTITUDE_M", plan.CruiseAltitudeM); err != nil {
		return Config{}, err
	}
	if plan.ClimbFraction, err = envFloat("FLIGHT_CLIMB_FRACTION", plan.ClimbFraction); err != nil {
		return Config{}, err
	}
	if plan.DescentFraction, err = envFloat("FLIGHT_DESCENT_FRACTION", plan.DescentFraction); err != nil {
		return Config{}, err
	}

	if err := plan.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid flight plan: %w", err)
	}

	return Config{
		Plan:       plan,
		RunID:      os.Getenv("FLIGHT_RUN_ID"),
		ListenAddr: envString("LISTEN_ADDR", "0.0.0.0:8080"),
	}, nil
}

func envString(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envFloat(name string, def float64) (float64, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", name, err)
	}
	return f, nil
}
