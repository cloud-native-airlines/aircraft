// Package engine applies ticks to a flight plan. It owns the small amount of
// mutable state the pure calculation does not: which run this flight is bound
// to, the high-water mark (latest tick applied), and the last computed state.
// All tick handling is serialized so concurrent requests cannot interleave.
package engine

import (
	"errors"
	"sync"
	"time"

	"github.com/cloud-native-airlines/flight/internal/flight"
)

// Errors returned by Apply; the HTTP layer maps both to 409 Conflict.
var (
	// ErrConflict is a tick that reuses a (run, tick_id) with a different time.
	ErrConflict = errors.New("conflicting tick content for an existing tick id")
	// ErrWrongRun is a tick for a run this flight is not bound to.
	ErrWrongRun = errors.New("tick for a different run")
)

// Tick is an incoming request to advance the flight's view of time.
type Tick struct {
	RunID       string
	TickID      int64
	SimulatedAt time.Time
}

// Result is the outcome of applying a tick.
type Result struct {
	State    flight.State
	Applied  bool // true only when this tick advanced the high-water mark
	Terminal bool // true when an applied tick reached arrival (flight should exit)
}

// Engine is safe for concurrent use.
type Engine struct {
	plan flight.Plan

	mu         sync.Mutex
	boundRun   string
	hasRun     bool
	hasApplied bool
	lastTickID int64
	lastState  flight.State
	seen       map[int64]time.Time // tick_id -> simulated time, for conflict/duplicate detection
}

// New creates an engine for a plan. If runID is non-empty the flight is bound to
// that run immediately; otherwise it binds to the first tick it receives.
func New(plan flight.Plan, runID string) *Engine {
	e := &Engine{plan: plan, seen: make(map[int64]time.Time)}
	if runID != "" {
		e.boundRun = runID
		e.hasRun = true
	}
	return e
}

// Plan returns the flight plan.
func (e *Engine) Plan() flight.Plan { return e.plan }

// RunID returns the bound run (empty until the first tick binds it).
func (e *Engine) RunID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.boundRun
}

// Current returns the latest applied state and whether any tick has been applied.
func (e *Engine) Current() (flight.State, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastState, e.hasApplied
}

// LastTickID returns the tick id of the latest applied tick.
func (e *Engine) LastTickID() (int64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastTickID, e.hasApplied
}

// Apply handles a tick per the ordering and conflict rules in FLIGHT_DESIGN.md.
func (e *Engine) Apply(t Tick) (Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Bind the run on first use; reject any other run thereafter.
	if !e.hasRun {
		e.boundRun = t.RunID
		e.hasRun = true
	} else if t.RunID != e.boundRun {
		return Result{}, ErrWrongRun
	}

	// Duplicate vs conflict for a tick id we've already seen.
	if prev, ok := e.seen[t.TickID]; ok {
		if !prev.Equal(t.SimulatedAt) {
			return Result{}, ErrConflict
		}
		// Exact duplicate: recompute deterministically, do not change high-water.
		return Result{State: flight.Calculate(e.plan, t.SimulatedAt), Applied: false}, nil
	}
	e.seen[t.TickID] = t.SimulatedAt

	state := flight.Calculate(e.plan, t.SimulatedAt)

	// A strictly newer tick id advances current state; older ones compute and
	// return but must not rewind the high-water mark.
	if !e.hasApplied || t.TickID > e.lastTickID {
		e.hasApplied = true
		e.lastTickID = t.TickID
		e.lastState = state
		return Result{State: state, Applied: true, Terminal: state.Terminal()}, nil
	}
	return Result{State: state, Applied: false}, nil
}
