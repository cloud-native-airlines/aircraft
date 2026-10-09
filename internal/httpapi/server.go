// Package httpapi exposes the flight engine over HTTP: ticks in, state out.
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/cloud-native-airlines/flight/internal/engine"
	"github.com/cloud-native-airlines/flight/internal/flight"
)

// Server wires the engine to HTTP handlers.
type Server struct {
	engine *engine.Engine
	log    *slog.Logger
	// onLanding is called once, after the response to the tick that lands the
	// flight is written, so the process can record final state and exit.
	onLanding func()
}

// New returns a Server. onLanding may be nil.
func New(e *engine.Engine, log *slog.Logger, onLanding func()) *Server {
	return &Server{engine: e, log: log, onLanding: onLanding}
}

// Handler builds the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.health)
	mux.HandleFunc("POST /tick", s.postTick)
	mux.HandleFunc("GET /state", s.getState)
	mux.HandleFunc("GET /flightplan", s.getFlightPlan)
	return mux
}

type tickRequest struct {
	RunID       string    `json:"run_id"`
	TickID      int64     `json:"tick_id"`
	SimulatedAt time.Time `json:"simulated_at"`
}

// stateResponse uses ADS-B field names so a Phase 2 report is a near-direct map.
type stateResponse struct {
	RunID          string  `json:"run_id"`
	TickID         int64   `json:"tick_id"`
	AircraftID     string  `json:"aircraft_id"`
	FlightID       string  `json:"flight_id"`
	SimulatedAt    string  `json:"simulated_at"`
	Latitude       float64 `json:"latitude"`
	Longitude      float64 `json:"longitude"`
	AltitudeM      float64 `json:"altitude_m"`
	GroundSpeedMPS float64 `json:"ground_speed_mps"`
	HeadingDeg     float64 `json:"heading_deg"`
	Status         string  `json:"status"`
	Phase          string  `json:"phase"`
	Applied        bool    `json:"applied"`
}

func (s *Server) response(runID string, tickID int64, st flight.State, applied bool) stateResponse {
	plan := s.engine.Plan()
	return stateResponse{
		RunID: runID, TickID: tickID,
		AircraftID: plan.AircraftID, FlightID: plan.FlightID,
		SimulatedAt:    st.SimulatedAt.UTC().Format(time.RFC3339),
		Latitude:       st.Position.Lat,
		Longitude:      st.Position.Lon,
		AltitudeM:      st.AltitudeM,
		GroundSpeedMPS: st.GroundMPS,
		HeadingDeg:     st.HeadingDeg,
		Status:         string(st.Status),
		Phase:          string(st.Phase),
		Applied:        applied,
	}
}

func (s *Server) postTick(w http.ResponseWriter, r *http.Request) {
	var req tickRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "could not decode tick JSON")
		return
	}
	if req.RunID == "" || req.SimulatedAt.IsZero() {
		writeError(w, http.StatusBadRequest, "invalid_tick", "run_id and simulated_at are required")
		return
	}

	res, err := s.engine.Apply(engine.Tick{RunID: req.RunID, TickID: req.TickID, SimulatedAt: req.SimulatedAt})
	switch {
	case errors.Is(err, engine.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "tick id already seen with different content")
		return
	case errors.Is(err, engine.ErrWrongRun):
		writeError(w, http.StatusConflict, "wrong_run", "tick is for a different run")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	s.log.Info("tick",
		"run_id", req.RunID, "tick_id", req.TickID,
		"aircraft_id", s.engine.Plan().AircraftID, "flight_id", s.engine.Plan().FlightID,
		"simulated_at", res.State.SimulatedAt.UTC().Format(time.RFC3339),
		"status", res.State.Status, "phase", res.State.Phase,
		"applied", res.Applied, "terminal", res.Terminal,
	)

	writeJSON(w, http.StatusOK, s.response(req.RunID, req.TickID, res.State, res.Applied))

	if res.Terminal {
		s.log.Info("flight landed; final state recorded, shutting down",
			"run_id", req.RunID, "flight_id", s.engine.Plan().FlightID,
			"aircraft_id", s.engine.Plan().AircraftID)
		if s.onLanding != nil {
			s.onLanding()
		}
	}
}

func (s *Server) getState(w http.ResponseWriter, r *http.Request) {
	st, ok := s.engine.Current()
	if !ok {
		writeError(w, http.StatusNotFound, "no_state", "no tick has been applied yet")
		return
	}
	tickID, _ := s.engine.LastTickID()
	writeJSON(w, http.StatusOK, s.response(s.engine.RunID(), tickID, st, true))
}

func (s *Server) getFlightPlan(w http.ResponseWriter, r *http.Request) {
	p := s.engine.Plan()
	writeJSON(w, http.StatusOK, map[string]any{
		"flight_id":         p.FlightID,
		"aircraft_id":       p.AircraftID,
		"origin":            map[string]float64{"latitude": p.Origin.Lat, "longitude": p.Origin.Lon},
		"destination":       map[string]float64{"latitude": p.Destination.Lat, "longitude": p.Destination.Lon},
		"departure_time":    p.Departure.UTC().Format(time.RFC3339),
		"arrival_time":      p.Arrival.UTC().Format(time.RFC3339),
		"cruise_altitude_m": p.CruiseAltitudeM,
		"climb_fraction":    p.ClimbFraction,
		"descent_fraction":  p.DescentFraction,
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]string{"code": kind, "message": msg}})
}
