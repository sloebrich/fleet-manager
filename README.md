# Fault-Tolerant Fleet Manager

A deliberately small Go distributed-systems project demonstrating how independent processes coordinate when communication is unreliable and processes can fail.

The fleet domain is intentionally simple: vehicles are independent processes moving on a 2D integer grid toward task destinations. The interesting part is **distributed coordination**, not fleet optimization.

## What this project demonstrates

- Independent Go processes communicating over MQTT pub/sub
- Goroutine-based concurrency
- Automatic heartbeats and failure detection
- Dropped, delayed, and duplicated messages (deterministic fault injection)
- Sequence numbers and stale-heartbeat handling
- Generation-based stale-assignment handling on the vehicle
- Task requeue and reassignment after vehicle failure
- Vehicle reconnection (an offline vehicle is marked online again on its next heartbeat)
- A minimal browser visualization

---

# 1. Architecture

The system consists of a central controller and multiple independent vehicle processes, coordinating via an MQTT broker.

```text
                     ┌────────────────────┐
                     │     Web UI          │
                     │  HTML/CSS/JS        │
                     └─────────┬──────────┘
                               │
                               │ HTTP
                               │
                     ┌─────────▼──────────┐
                     │    MQTT Broker      │
                     │  (mosquitto, etc)   │
                     └───────┬───────┬────┘
                             │       │
                       MQTT  │       │  MQTT
                             │       │
                     ┌───────▼───┐ ┌─▼─────────┐
                     │Controller  │ │ Vehicle 1 │
                     │            │ │           │
                     │ authorit.  │ │ movement  │
                     │ state      │ │ heartbeat │
                     │ scheduler  │ │ publisher │
                     │ detector   │ │           │
                     │ http api   │ │           │
                     └────────────┘ └─────┬─────┘
                                          │
                                     ┌─────▼─────┐
                                     │ Vehicle 2 │
                                     │           │
                                     │ movement  │
                                     │ heartbeat │
                                     │ publisher │
                                     └───────────┘
```

The controller is the **authoritative source of truth** for:

- vehicle state
- vehicle position
- task state
- task assignment
- assignment generation

Vehicles are relatively dumb agents. They:

- move locally
- publish position updates via heartbeat
- publish heartbeats automatically
- execute assignments

---

# 2. Repository structure

```text
fleet-manager/
│
├── cmd/
│   ├── controller/
│   │   └── main.go
│   │
│   └── vehicle/
│       └── main.go
│
├── src/
│   ├── config/
│   │   ├── scenario.go
│   │   └── scenario_test.go
│   │
│   ├── controller/
│   │   ├── controller.go
│   │   └── http.go
│   │
│   ├── domain/
│   │   └── types.go
│   │
│   ├── fault/
│   │   └── injector.go
│   │
│   ├── mqtt/
│   │   └── client.go
│   │
│   └── vehicle/
│       └── vehicle.go
│
├── scenarios/
│   └── scenario.json
│
├── web/
│   └── index.html
│
├── docker/
│   └── mosquitto/
│       └── mosquitto.conf
│
├── Dockerfile
├── docker-compose.yml
├── README.md
├── go.mod
├── go.sum
```

---

# 3. Message protocol

Communication is JSON-encoded and sent over MQTT topics.

## Vehicle → Controller

**Heartbeat** (published every second)
- Topic: `heartbeat/{vehicle_id}`
- Payload: JSON with vehicleId, position, sequence, currentTask

## Controller → Vehicle

**AssignTask** (published when task is assigned)
- Topic: `task/{vehicle_id}`
- Payload: JSON with taskId, destination, generation, sequence

**Stop** (published when task is reassigned or vehicle fails)
- Topic: `stop/{vehicle_id}`
- Payload: JSON with reason, sequence

The `sequence` on controller → vehicle messages is a per-vehicle counter shared by AssignTask and Stop. Fault rules match on it.

---

# 4. Message definitions (JSON)

All messages are JSON. Vehicles and controller encode/decode them.

## Heartbeat

Published by vehicle to `heartbeat/{vehicle_id}`:

```json
{
  "vehicleId": "vehicle-1",
  "position": {
    "x": 5,
    "y": 3
  },
  "sequence": 42,
  "currentTask": "task-1"
}
```

## AssignTask

Published by controller to `task/{vehicle_id}`:

```json
{
  "taskId": "task-1",
  "destination": {
    "x": 10,
    "y": 5
  },
  "generation": 1,
  "sequence": 1
}
```

## Stop

Published by controller to `stop/{vehicle_id}`:

```json
{
  "reason": "task reassigned",
  "sequence": 2
}
```

---

# 5. Message semantics

## Heartbeat

Vehicle publishes periodically:

```text
V1 → broker  heartbeat/vehicle-1
             {
               "vehicleId": "vehicle-1",
               "position": {"x": 7, "y": 3},
               "sequence": 41,
               "currentTask": "task-1"
             }

Controller subscribes to heartbeat/+
Receives on callback
Extracts position, sequence, currentTask
Updates authoritative state
Checks if task is complete
Records timestamp
```

The controller records the time at which the heartbeat was received.

Position updates are embedded in the heartbeat (no separate UpdatePosition message).

Heartbeats with a sequence number at or below the last one seen are treated as stale: they only refresh the timestamp, not the position or task state.

Each heartbeat also reports the vehicle's `currentTask`. If the controller has a task in progress for that vehicle and the vehicle reports a different one, the controller requeues the task and, if the vehicle is running something else, sends it a Stop.

---

## AssignTask

When a vehicle is idle and a task is queued, the controller publishes:

```text
C → broker  task/vehicle-1
            {
              "taskId": "task-1",
              "destination": {"x": 10, "y": 5},
              "generation": 1,
              "sequence": 1
            }

V1 subscribes to task/vehicle-1
Receives on callback
Extracts taskId, destination, generation
Sets destination
Begins moving
```

Generation identifies the version of the assignment. If a task is later reassigned:

```text
C → broker  task/vehicle-2
            {
              "taskId": "task-1",
              "destination": {"x": 10, "y": 5},
              "generation": 2,
              "sequence": 1
            }

V2 receives generation=2
V1 may still have generation=1 (stale)
V1 ignores if it receives generation=1 later (checks generation)
```

Generation 2 supersedes generation 1.

---

## Stop

The controller can tell a vehicle to stop executing an obsolete assignment.

```text
C → broker  stop/vehicle-1
            {
              "reason": "task reassigned",
              "sequence": 2
            }

V1 subscribes to stop/vehicle-1
Receives on callback
Clears destination
Stops moving
```

This is important after failure detection or reassignment.

---

# 6. Failure handling

A heartbeat timeout does **not** prove that a vehicle has crashed.

The controller only knows:

> "I have not heard from this vehicle."

This could mean:

- the vehicle crashed
- the vehicle is disconnected from the broker
- the network is partitioned
- messages are delayed
- the broker itself has a problem

Therefore the failure detector is necessarily imperfect.

## Controller-side timeout

If no heartbeat is received within the configured timeout:

```text
C:

V1 → OFFLINE

current task:
    IN_PROGRESS → QUEUED

scheduler:
    may assign task to another vehicle
```

---

# 7. Why no fencing tokens?

The project does not implement full fencing tokens or leases.

Assignment generations are enough to demonstrate stale assignment handling at the application level.

For example:

```text
T17 → V1
generation=1

V1 becomes unreachable

T17 → V2
generation=2
```

If V1 later reconnects, the controller knows that generation 1 is obsolete.

However, a completely disconnected V1 cannot learn about generation 2 while disconnected.

A production system with strong physical-safety requirements would need stronger mechanisms such as leases/fencing or another external authority.

That complexity is intentionally outside the project's scope.

---

# 8. Message faults

The project can simulate unreliable message delivery at the application level.

The MQTT broker itself delivers reliably (QoS 1), but both the controller and the vehicles can inject faults when they publish:

```text
Sender builds message
   │
   ▼
publish()
   │
   ├── DROP (don't send)
   ├── DELAY (send after N)
   └── DUPLICATE (send twice)
   │
   ▼
MQTT Broker (reliable delivery)
```

Faults are deterministic rules matched against the sender's own `sequence` number:

```go
type Rule struct {
    VehicleID string        // "" matches any vehicle
    Sequence  *int          // nil matches any sequence
    Action    Action        // Drop, Delay, Duplicate
    Delay     time.Duration // used by Delay
}
```

Vehicle rules affect heartbeats and are registered in `cmd/vehicle/main.go`. Controller rules affect AssignTask and Stop and are loaded from `scenarios/scenario.json`.

---

# 9. Scheduler

Tasks are assigned to the idle vehicle with the smallest Manhattan distance to the destination.

For example:

```text
Task T1:
    destination=(10,10)

V1:
    position=(8,10)
    distance=2

V2:
    position=(3,3)
    distance=14

→ assign T1 to V1
```

---

# 10. Next

- Reordering fault (hold a message and release it out of order)
- Vehicle-side broker timeout: stop moving when no messages arrive from the broker
- Controller persistence: save state to disk and reconcile with vehicles on restart
- Deterministic integration tests for the fault scenarios (only the scenario loader has a unit test today)
