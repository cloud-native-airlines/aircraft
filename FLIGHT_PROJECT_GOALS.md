# Cloud Native Airlines Flight Project Goals

> **Flight vs. Aircraft.** This service is a **Flight**: it executes one flight
> leg over a simulated time window, then exits. The persistent **aircraft**
> identity (a tail number that can only be in one place and must depart where it
> last landed) is owned by **Operations**, which tracks the fleet and assigns
> legs. The Flight emits departure/arrival events Operations consumes. See
> [FLIGHT_DESIGN.md](FLIGHT_DESIGN.md) for the model.

## Purpose

The Flight service executes one flight leg in the Cloud Native Airlines simulated world. It follows a flight plan, calculates its physical state at a supplied simulated time, and reports that state so other services can show where it is and what it is doing.

Cloud Native Airlines is an observability demo. Each flight will eventually run in its own Kubernetes pod, making individual flights visible both on a map and in infrastructure telemetry. A reporting outage, slow request, or pod restart should create behavior that someone can investigate through logs, metrics, and traces.

The goal is a predictable flight simulation that produces useful application traffic and telemetry. A simple, coherent flight model is sufficient for the initial demo; detailed aerodynamics can be added when they create a useful scenario.

## Initial scope

Start with one fixed flight from Minneapolis (MSP) to Chicago O'Hare (ORD). Load the flight plan (including the aircraft identity it is flown by) from configuration. Accept manually supplied ticks through an HTTP endpoint and return and log the calculated state.

The first milestone runs locally without Simulator, Operations, ADS-B, or Kubernetes. The next integration step sends reports to ADS-B and accepts automatic ticks from Simulator. Go is the proposed implementation language, with the flight calculation kept separate from HTTP handling and report delivery.

## Flight model

A flight plan defines the flight identifier, departure and destination coordinates, departure time, arrival time, and parameters for a simple climb, cruise, and descent profile. Times belong to the simulated world. Coordinates and movement fields use explicit units.

| Phase | Expected behavior |
| --- | --- |
| Before departure | Parked at the origin with zero altitude and ground speed |
| Climb | Moving along the route while altitude rises |
| Cruise | Moving along the route at the configured cruising altitude |
| Descent | Continuing toward the destination while altitude falls |
| At and after arrival | At the destination with zero altitude and ground speed |

Use a great-circle route for position calculations. The timing and speed profile should agree with distance traveled: reported speed should describe the simulated movement. Define the simplified altitude reference and document assumptions about airport elevation. Heading describes travel direction in degrees; document its value while stationary.

Lifecycle status and flight phase are separate concepts. Initial reports can use `parked` and `flying` as status values while logs or state details describe climb, cruise, and descent. Turnaround and multiple assignments follow in a later milestone.

## Tick handling and deterministic behavior

Simulator owns the clock, speed, and tick interval. The Flight receives a run ID, tick ID, and absolute simulated timestamp. It calculates state from that timestamp and its flight plan, rather than accumulating distance each time a request arrives.

The same plan and simulated time must produce the same physical state. Repeated ticks must not move the aircraft twice, and a missed tick must not permanently leave it behind. A jump in simulated time can skip intermediate reports, but the next state must still be correct.

Handle requests concurrently without allowing an older tick to replace newer current state. Define responses for duplicate ticks, conflicting tick contents, and out-of-order ticks. Switching runs requires an explicit initialization or reset rule so a delayed request from an earlier run cannot reactivate that run.

Pause does not require the Flight to own a pause control. It follows the supplied time; when simulated time stops advancing, physical state remains unchanged. Simulator decides whether to continue sending unchanged-time ticks while paused.

## Position reporting

When ADS-B integration is added, send reports through its ingestion API. ADS-B owns position storage and queries. The Flight owns calculating and delivering its report.

| Report data | Meaning |
| --- | --- |
| Run, tick, aircraft, and flight identifiers | Identify the simulation and flight being reported |
| Report identifier | Stable identifier reused for retries of the same report |
| Simulated timestamp | Time represented by the calculated state |
| Latitude and longitude | Position in decimal degrees |
| Altitude | Meters under the documented altitude convention |
| Ground speed | Meters per second |
| Heading | Degrees from 0 inclusive to 360 exclusive |
| Status | Parked, flying, or later turnaround |

Use the implemented ADS-B contract when connecting the services. ADS-B adds real receipt time. Retry a report with the same identifier and payload, using bounded deadlines and retry limits. A reporting failure must be observable and must not change the aircraft's physical state. Durable buffering and replay of every missed report can be deferred.

## Component boundaries

| Component | Responsibility |
| --- | --- |
| Flight | Calculate physical state, handle ticks, deliver reports, and emit departure/arrival events |
| Simulator | Advance simulation time and deliver ticks |
| Operations | Own the aircraft registry: fleet records, routes, schedules, assignments, and operational flight status — including each aircraft's current location and the rule that it flies one leg at a time, departing where it last landed |
| ADS-B | Store position reports and expose positions and flight tracks |
| Flight Tracker | Display aircraft positions and reporting freshness |
| FIDS | Display airport departures and arrivals |

A Flight obtains its assignment (plan) from Operations and sends it departure and arrival events. Those events are distinct from transponder reports, allowing a reporting outage to coexist with a flight that is still operating. Operations uses the events to update the aircraft's location and release its next leg; the Flight itself cannot teleport because it is only ever told to fly one leg between two fixed points.

## Kubernetes lifecycle

A Flight process executes one leg and then exits — it maps to a Kubernetes Job that completes on landing. Operations launches the next leg for an aircraft as a new Flight. The set of running Flight pods is the set of flights currently in the air.

A crashed or replaced Flight pod resumes the same leg: with its flight plan and the current simulation time, the restarted process calculates the aircraft's current position instead of restarting the flight from its origin. Stable aircraft identity comes from the plan supplied at launch.

The main repository owns Kubernetes packaging and fleet provisioning. The Flight repository provides a container, health endpoints, configuration, and graceful shutdown behavior. Kubernetes deployment is a later integration milestone, not a prerequisite for testing flight calculations.

## Observability goals

Telemetry should explain whether the Flight is receiving ticks, calculating state successfully, and delivering fresh reports. Measure tick processing time, calculation failures, report delivery latency, delivery failures, and retries in real time.

Structured logs include run, tick, aircraft, and flight identifiers. Propagate trace context from incoming ticks into outgoing ADS-B requests. Use OpenTelemetry with configurable export, allowing local use without a collector. Keep identifiers in logs and traces rather than introducing unbounded metric labels.

Useful demonstrations include missed ticks, duplicate delivery, an unavailable ADS-B service, a slow reporting request, and a pod restart. Each should preserve understandable simulation behavior while exposing the failure through telemetry.

## First milestone success criteria

- One configured MSP-to-ORD flight runs locally and accepts manual ticks.
- Before departure and after arrival, coordinates match the appropriate airport and altitude and speed are zero.
- Intermediate ticks produce coherent route, altitude, speed, and heading values.
- The calculation is deterministic and can be tested without HTTP or external services.
- Duplicate, older, and wrong-run ticks cannot accidentally advance or rewind current state.
- On reaching arrival, the Flight records its final state and exits.
- Logs explain which run, tick, aircraft, and flight produced each state.
- A container build and README commands demonstrate departure, an intermediate point, and arrival.
- Meaningful tests cover time boundaries, intermediate movement, skipped ticks, and request ordering.

## Future development

Add ADS-B reporting and automatic Simulator ticks first. Then introduce Operations assignments, turnaround, and several flights per aircraft. Later scenarios can model aircraft performance differences, weather effects, equipment failures, or maintenance restrictions while keeping ownership of schedules and business decisions in Operations.

Each extension should make the demo more observable and easier to explain, with a runnable example showing the resulting behavior.
