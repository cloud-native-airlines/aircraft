// Package ticks subscribes to the Simulator's tick stream on NATS and feeds each
// tick into the same engine the HTTP endpoint uses. On an applied tick it
// delivers a position report to ADS-B; on the landing tick it signals shutdown.
package ticks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/cloud-native-airlines/flight/internal/adsb"
	"github.com/cloud-native-airlines/flight/internal/engine"
	"github.com/cloud-native-airlines/flight/internal/flight"
	"github.com/nats-io/nats.go"
)

// Reporter delivers a report to ADS-B. *adsb.Client satisfies it; nil disables
// reporting (ticks are still applied and logged).
type Reporter interface {
	Send(ctx context.Context, r adsb.Report) error
}

type tickMessage struct {
	RunID       string    `json:"run_id"`
	TickID      int64     `json:"tick_id"`
	SimulatedAt time.Time `json:"simulated_at"`
	Paused      bool      `json:"paused"`
}

// Subscriber bridges NATS ticks to the engine.
type Subscriber struct {
	conn      *nats.Conn
	sub       *nats.Subscription
	engine    *engine.Engine
	reporter  Reporter
	log       *slog.Logger
	onLanding func()
}

// Connect dials NATS and subscribes to the tick subject. reporter may be nil.
func Connect(url, subject string, eng *engine.Engine, reporter Reporter, log *slog.Logger, onLanding func()) (*Subscriber, error) {
	conn, err := nats.Connect(url,
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			log.Warn("nats disconnected", "error", err)
		}),
		nats.ReconnectHandler(func(c *nats.Conn) {
			log.Info("nats reconnected", "url", c.ConnectedUrl())
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}

	s := &Subscriber{conn: conn, engine: eng, reporter: reporter, log: log, onLanding: onLanding}
	sub, err := conn.Subscribe(subject, s.handle)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("subscribe %q: %w", subject, err)
	}
	s.sub = sub
	log.Info("subscribed to ticks", "subject", subject, "url", conn.ConnectedUrl(), "reporting", reporter != nil)
	return s, nil
}

func (s *Subscriber) handle(msg *nats.Msg) {
	var tick tickMessage
	if err := json.Unmarshal(msg.Data, &tick); err != nil {
		s.log.Warn("bad tick message", "error", err)
		return
	}

	res, err := s.engine.Apply(engine.Tick{RunID: tick.RunID, TickID: tick.TickID, SimulatedAt: tick.SimulatedAt})
	if err != nil {
		// Wrong-run / conflicting ticks are expected noise, not failures.
		s.log.Warn("tick rejected", "run_id", tick.RunID, "tick_id", tick.TickID, "error", err)
		return
	}

	plan := s.engine.Plan()
	s.log.Info("tick",
		"run_id", tick.RunID, "tick_id", tick.TickID,
		"aircraft_id", plan.AircraftID, "flight_id", plan.FlightID,
		"simulated_at", res.State.SimulatedAt.UTC().Format(time.RFC3339),
		"status", res.State.Status, "phase", res.State.Phase,
		"applied", res.Applied, "terminal", res.Terminal,
	)

	if res.Applied && s.reporter != nil {
		s.report(tick, res.State, plan)
	}
	if res.Terminal && s.onLanding != nil {
		s.log.Info("flight landed; final report delivered, shutting down",
			"run_id", tick.RunID, "flight_id", plan.FlightID, "aircraft_id", plan.AircraftID)
		s.onLanding()
	}
}

func (s *Subscriber) report(tick tickMessage, st flight.State, plan flight.Plan) {
	report := adsb.Report{
		ReportID:       fmt.Sprintf("%s-%s-%06d", tick.RunID, plan.AircraftID, tick.TickID),
		RunID:          tick.RunID,
		TickID:         tick.TickID,
		AircraftID:     plan.AircraftID,
		FlightID:       plan.FlightID,
		SimulatedAt:    st.SimulatedAt.UTC().Format(time.RFC3339),
		Latitude:       st.Position.Lat,
		Longitude:      st.Position.Lon,
		AltitudeM:      st.AltitudeM,
		GroundSpeedMPS: st.GroundMPS,
		HeadingDeg:     st.HeadingDeg,
		Status:         string(st.Status),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.reporter.Send(ctx, report); err != nil {
		// Observable, but does not change physical state.
		s.log.Error("report delivery failed",
			"run_id", tick.RunID, "tick_id", tick.TickID, "report_id", report.ReportID, "error", err)
	}
}

// Close unsubscribes and drains the connection.
func (s *Subscriber) Close() {
	if s.sub != nil {
		_ = s.sub.Unsubscribe()
	}
	if s.conn != nil {
		_ = s.conn.Drain()
	}
}
