# Cloud Native Airlines Flight — Design & Plan

Companion to [FLIGHT_PROJECT_GOALS.md](FLIGHT_PROJECT_GOALS.md). Records the design
decisions for the first version and the plan to build it.

## Flight vs. Aircraft

This service is a **Flight**: it executes **one leg** (e.g. MSP→ORD) over a
simulated time window, then exits. An **Aircraft** (tail number) is a persistent
identity with physical continuity — it can only be in one place, can't fly two
legs at once, and its next leg must depart where the last one landed.

Those continuity rules are **scheduling invariants**, and the component-boundary
table already assigns them to **Operations** (*fleet records, routes, schedules,
assignments, operational flight status*). So the "aircraft registry" lives in
Operations, not here. A Flight is handed a complete, consistent plan and simply
flies it — it cannot teleport because it is only ever told to fly between two
fixed points. The connective tissue is **lifecycle events** (departed / arrived)
that the Flight emits and Operations consumes to update each aircraft's location
and release its next leg.

| Concept | Owner | Lifetime | Responsibility |
| --- | --- | --- | --- |
| **Flight** (this service) | one pod/Job per leg | ephemeral — exits on landing | Execute a plan, report to ADS-B, emit departure/arrival events |
| **Aircraft registry** | Operations | persistent | Track tail → location/assignment; only release legs that start where the aircraft is |

## Decisions at a glance

| Topic | Decision | Rationale |
| --- | --- | --- |
| Language | **Go** | Per the goals; makes the demo polyglot (Simulator=Python, ADS-B=Rust, Flight=Go). |
| Unit of execution | **One flight leg per process; exit on landing** | Maps to a Kubernetes **Job**. Running pods == flights currently in the air — a clean observability story. |
| State model | **Pure function of `(plan, simulated_at)`** | Determinism, idempotent duplicate ticks, self-correcting missed ticks, and mid-flight restart all fall out for free. |
| Flight model | **Trapezoidal speed + altitude profile** | Speed ramps 0→cruise→0; cruise speed derived so distance integrates to the great-circle length. Speed genuinely describes the movement. |
| Run binding | **A Flight is bound to a single `run_id`** | No run-switching; a reset means Operations launches new Flight pods. A tick for any other run is rejected. |
| Registry / continuity | **Operations owns it; the Flight only emits events** | Matches component boundaries; keeps the Flight a dumb, safe executor. |
| Tick transport | **HTTP `POST /tick` in Phase 1; NATS subscriber in Phase 2** | Milestone 1 accepts manual ticks locally; both paths feed one engine. |

## Component layout

The flight calculation is isolated from HTTP and networking so it can be tested
without any external service (a milestone criterion).

```
flight/
├── cmd/flight/main.go          wiring: config → engine → HTTP server; exit on landing
├── internal/flight/            PURE calc — no HTTP, no network
│   ├── geo.go                  great-circle distance, interpolation, bearing
│   ├── plan.go                 flight plan + profile parameters
│   └── state.go                State(plan, simulated_at) → physical state
├── internal/engine/            tick engine: run binding, high-water, ordering, terminal
├── internal/httpapi/           handlers over the engine
└── internal/config/            identity + flight plan loading
```

## Flight model (trapezoidal)

A flight plan defines: flight & aircraft identifiers, origin and destination
coordinates, `departure_time` and `arrival_time` (simulated), `cruise_altitude_m`,
and climb/descent as fractions of the total flight time (defaults: climb `0.25`,
descent `0.25`, cruise the remainder).

Let `T = arrival − departure`, split into climb `Tc`, cruise `Tcr`, descent `Td`.
Let `D` be the great-circle distance between the airports.

**Ground speed** is a trapezoid: `0 → vmax` over climb, constant `vmax` over cruise,
`vmax → 0` over descent. `vmax` is derived so the area equals the distance:

```
vmax = D / (Tc/2 + Tcr + Td/2)
```

**Along-track distance** `s(t)` is the integral of that speed (piecewise), so
position stays consistent with reported speed:

```
climb    (0 ≤ t ≤ Tc):         s = vmax · t² / (2·Tc)
cruise   (Tc < t ≤ Tc+Tcr):    s = vmax·Tc/2 + vmax·(t − Tc)
descent  (t in last Td):       s = D − vmax·(Td − t_d)² / (2·Td),  t_d = t − (T − Td)
```

**Position** is the great-circle point at fraction `s/D` from origin to destination
(spherical interpolation). **Altitude** is a linear trapezoid in time: `0 → cruise_altitude_m`
over climb, constant over cruise, `→ 0` over descent.

**Heading** is the instantaneous great-circle bearing from the current position
toward the destination, in `[0, 360)`. While stationary (before departure / at
arrival), heading holds the route's initial bearing (documented; matches the
sample data's constant ~264°).

**Phases & status.** Status is the lifecycle value ADS-B stores
(`parked` / `flying`; `turnaround` later). Phase is the finer movement detail in
logs/state (`parked`, `climb`, `cruise`, `descent`, `arrived`).

| Simulated time | Status | Phase | Position / altitude / speed |
| --- | --- | --- | --- |
| before departure | `parked` | `parked` | origin · 0 · 0 |
| climb / cruise / descent | `flying` | `climb`/`cruise`/`descent` | along route · profile · profile |
| at and after arrival | `parked` | `arrived` | destination · 0 · 0 |

**Altitude reference:** airports are treated as sea level (elevation 0); altitude
is metres above that reference. This is a documented simplification.

## Lifecycle: park → fly → land → exit

A Flight pod is launched before departure, sits parked, flies when simulated time
crosses `departure_time`, and **exits once an applied tick reaches `arrival_time`**.

1. **On the first applied tick with sim time ≥ `departure_time`** → emit a
   **departure event** (Phase 2) as the status crosses `parked → flying`.
2. **On the first applied tick with sim time ≥ `arrival_time`** → compute the
   terminal state (at the destination, zero altitude/speed), **record the final
   state** (log it in Phase 1; final ADS-B report + **arrival event** in Phase 2),
   return the HTTP response, then **gracefully shut down and exit 0**.

Because state is a pure function of time, this is correct even under jumps and
restarts: a pod that cold-starts after `arrival_time` immediately computes the
arrived state, records it, and exits; a crash mid-flight restarts the Job, which
recomputes position from the current simulated time and continues — it never
restarts the flight from the origin.

## Tick handling and determinism

A tick carries `{run_id, tick_id, simulated_at}` (extra Simulator fields such as
`paused`/`speed` are ignored — when time does not advance, the computed state
simply does not change). The engine computes state purely from `simulated_at`.
The only stored state is the **bound run**, a **high-water mark** (latest tick
applied), and the last computed state (for `GET /state`). All tick handling is
mutex-guarded so concurrent requests cannot interleave.

A Flight **binds to a single run**: the run comes from config (`FLIGHT_RUN_ID`) if
set, otherwise from the first tick received.

| Case | Rule | Response |
| --- | --- | --- |
| First tick (no bound run) | bind the run, apply | `200` + state |
| Newer tick, bound run | compute, advance high-water, cache | `200` + state (`applied:true`) |
| Duplicate tick (same run+tick_id+time) | recompute (deterministic); high-water unchanged | `200` + state (idempotent) |
| Older / out-of-order tick, bound run | compute and **return** that time's state, but do **not** regress current | `200` + state (`applied:false`) |
| Conflicting content (same run+tick_id, different `simulated_at`) | reject | `409 Conflict` |
| Tick for a different `run_id` | reject — the Flight serves one run; a reset is a new pod | `409 Conflict` |

This satisfies the goals: repeated ticks don't move the aircraft twice, a missed
tick doesn't leave it behind, a time jump skips intermediate reports but lands
correct, and a delayed tick from another run cannot affect this Flight.

## HTTP API (Phase 1)

| Method & path | Purpose |
| --- | --- |
| `POST /tick` | Apply a tick `{run_id, tick_id, simulated_at}`; returns the computed state |
| `GET /state` | Latest applied state (bound-run high-water), or `404` before the first tick |
| `GET /flightplan` | The loaded plan (debug) |
| `GET /healthz` · `/readyz` | Liveness / readiness |

The state payload uses ADS-B field names so a Phase 2 report is a near-direct map:

```json
{
  "run_id": "...", "tick_id": 6, "aircraft_id": "N100CA", "flight_id": "CNA100",
  "simulated_at": "2026-10-06T14:06:00Z",
  "latitude": 43.45, "longitude": -90.55,
  "altitude_m": 10668, "ground_speed_mps": 231.5, "heading_deg": 264,
  "status": "flying", "phase": "cruise", "applied": true
}
```

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `FLIGHT_PLAN` | — | Path to a flight-plan JSON (origin/destination/times/flight/aircraft) |
| `FLIGHT_RUN_ID` | — | Bind to this run; if unset, bind to the first tick's run |
| `FLIGHT_CRUISE_ALTITUDE_M` | `10668` | Cruise altitude (FL350) |
| `FLIGHT_CLIMB_FRACTION` / `FLIGHT_DESCENT_FRACTION` | `0.25` / `0.25` | Phase split of total flight time |
| `LISTEN_ADDR` | `0.0.0.0:8080` | HTTP listen address |

The flight-plan file reuses the shape of the main repo's
[scenarios/msp-ord.json](../cloud-native-airlines/scenarios/msp-ord.json)
(origin, destination, `departure_time`, `arrival_time`, `flight_id`,
`aircraft_id`), plus the profile fields above.

## Build plan

### Phase 1 — local flight calculation, tick endpoint, exit on landing

1. **Module & skeleton** — `go.mod`, `cmd/flight`, config loading, `/healthz`
   `/readyz`, Dockerfile, Makefile, CI workflows (mirroring ADS-B/Simulator).
2. **`internal/flight`** — great-circle geo, the trapezoidal profile, and
   `State(plan, t)`; table-driven unit tests for the boundaries (before departure,
   arrival, climb/cruise/descent midpoints) with no HTTP.
3. **`internal/engine`** — tick application with run binding, high-water, the
   ordering/conflict rules, and terminal detection; unit tests for duplicate,
   older, conflicting, out-of-order, wrong-run, and landing cases.
4. **`internal/httpapi`** — `POST /tick`, `GET /state`, `GET /flightplan`; structured
   logs carrying run/tick/aircraft/flight; graceful shutdown on landing.
5. **Runnable** — a README showing a manual flow: a parked tick, an intermediate
   tick, and an arrival tick (which lands, logs final state, and exits).

**Phase 1 done when:** one configured MSP→ORD flight runs locally, accepts manual
ticks, reports zero altitude/speed at the airports and coherent values between,
records its final state and exits on landing, the calc is deterministic and tested
without HTTP, duplicate/older/wrong-run ticks cannot advance or rewind current
state, and logs identify run/tick/aircraft/flight.

### Phase 2 — integration & observability

6. **ADS-B reporting** — after each applied tick, `POST /api/v1/reports` with a
   stable `report_id` (`{run_id}-{aircraft_id}-{tick_id}`), bounded deadline and
   retry limit, idempotent retries. A delivery failure is observable and does
   **not** change physical state.
7. **Lifecycle events to Operations** — emit departure/arrival events (distinct
   from transponder reports) so Operations can update the aircraft registry and
   release the next leg.
8. **Automatic ticks from Simulator** — a NATS subscriber feeding the same engine
   as the HTTP endpoint.
9. **OpenTelemetry** — traces (propagate context from the incoming tick into the
   ADS-B request), metrics (tick processing time, calc failures, delivery latency,
   failures, retries), configurable OTLP export with no collector required.
10. **Kubernetes Job lifecycle** — package as a Job that completes on landing;
    confirm a crashed pod resumes mid-flight from plan + current time.

### Later

Operations assignments and turnaround (the registry releasing sequential legs per
aircraft), then performance/weather/failure scenarios — ownership of schedules
stays in Operations.

## Open items

- Confirm the flight-plan file shape and whether the Flight reads the shared
  `scenarios/msp-ord.json` directly or a launch-supplied copy.
- Transport for lifecycle events to Operations (NATS vs HTTP) — decide in Phase 2.
- Default listen port `8080` collides with ADS-B when both run locally without
  containers — pick distinct host ports in the demo (compose/K8s handle this).
- The main repo's `ARCHITECTURE.md` still refers to `../aircraft`; update those
  references to `../flight` when that repo is revised.
