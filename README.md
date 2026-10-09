# Cloud Native Airlines Flight

Executes one flight leg in the Cloud Native Airlines simulated world: it accepts
ticks (an absolute simulated time), calculates the aircraft's physical state from
its flight plan, and **exits once the flight lands**. See
[FLIGHT_PROJECT_GOALS.md](FLIGHT_PROJECT_GOALS.md) for the vision and
[FLIGHT_DESIGN.md](FLIGHT_DESIGN.md) for the design and phased plan.

A **Flight** runs one leg and then exits (a Kubernetes Job). The persistent
**aircraft** identity and the "one place at a time / depart where you landed"
rule are owned by **Operations**, which the Flight feeds with departure/arrival
events (Phase 2).

**Phase 1 (this version):** local flight calculation and tick endpoint — no
Simulator, Operations, ADS-B, or Kubernetes. State is a pure function of
`(plan, simulated_time)`, so it is deterministic and tested without HTTP.

## Run

### Go

```sh
make run            # serves on :8080 using scenarios/msp-ord.json
# or:
FLIGHT_PLAN=scenarios/msp-ord.json go run ./cmd/flight
```

### Docker

```sh
make build-image
docker run --rm -p 8080:8080 cna-flight:0.1.0
```

## Demonstration

With the service running, deliver a few ticks (the sample plan flies MSP→ORD from
14:00 to 15:00 simulated):

```sh
# Parked before departure — at MSP, zero altitude and speed
curl -s -XPOST localhost:8080/tick \
  -d '{"run_id":"demo","tick_id":0,"simulated_at":"2026-10-06T14:00:00Z"}'

# Cruise — at altitude along the route (~199 m/s ground speed)
curl -s -XPOST localhost:8080/tick \
  -d '{"run_id":"demo","tick_id":30,"simulated_at":"2026-10-06T14:30:00Z"}'

# Arrival — lands at ORD, records final state, and the process exits
curl -s -XPOST localhost:8080/tick \
  -d '{"run_id":"demo","tick_id":60,"simulated_at":"2026-10-06T15:00:00Z"}'
```

`GET /state` returns the latest applied state between ticks.

## API

| Method & path | Purpose |
| --- | --- |
| `POST /tick` | Apply `{run_id, tick_id, simulated_at}`; returns the computed state |
| `GET /state` | Latest applied state, or `404` before the first tick |
| `GET /flightplan` | The loaded plan |
| `GET /healthz` · `/readyz` | Liveness / readiness |

The state payload uses ADS-B field names (`altitude_m`, `ground_speed_mps`,
`heading_deg`, `status`) so a Phase 2 report is a near-direct map. `applied` is
`true` only when the tick advanced current state.

**Tick rules** (see the design doc): a Flight binds to one `run_id`; a newer tick
advances state; a duplicate is idempotent; an older/out-of-order tick is computed
and returned but does not rewind current state; conflicting content for a known
tick id and ticks for another run return `409`.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `FLIGHT_PLAN` | — (required) | Path to a flight-plan JSON |
| `FLIGHT_RUN_ID` | — | Bind to this run; if unset, bind to the first tick's run |
| `FLIGHT_CRUISE_ALTITUDE_M` | `10668` | Cruise altitude (overrides the plan) |
| `FLIGHT_CLIMB_FRACTION` / `FLIGHT_DESCENT_FRACTION` | `0.25` / `0.25` | Phase split of total flight time |
| `LISTEN_ADDR` | `0.0.0.0:8080` | HTTP listen address |

### A note on simulated flight times

Flight plans use **simulated** time, and reported ground speed is derived so it
matches the actual great-circle movement over that time. The sample plan uses a
realistic 60-minute MSP→ORD leg (~540 km, so ~199 m/s cruise). Don't shorten the
simulated duration to make the demo quick — instead let the **Simulator's clock
speed** compress real time (e.g. 60× watches the full hour in one real minute),
which keeps the reported speeds believable.

## Test

```sh
make test
```
