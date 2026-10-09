// Command flight runs a single Cloud Native Airlines flight leg: it accepts
// ticks, calculates physical state, and exits once the flight lands.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/cloud-native-airlines/flight/internal/config"
	"github.com/cloud-native-airlines/flight/internal/engine"
	"github.com/cloud-native-airlines/flight/internal/httpapi"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		log.Error("configuration error", "error", err)
		os.Exit(1)
	}

	eng := engine.New(cfg.Plan, cfg.RunID)

	// Landing cancels this context, which triggers graceful shutdown. once
	// guards against the handler firing it more than once.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var once sync.Once
	onLanding := func() { once.Do(stop) }

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: httpapi.New(eng, log, onLanding).Handler(),
	}

	log.Info("flight starting",
		"flight_id", cfg.Plan.FlightID, "aircraft_id", cfg.Plan.AircraftID,
		"run_id", cfg.RunID, "listen_addr", cfg.ListenAddr,
		"departure", cfg.Plan.Departure.Format(time.RFC3339),
		"arrival", cfg.Plan.Arrival.Format(time.RFC3339),
	)

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown error", "error", err)
		os.Exit(1)
	}
}
