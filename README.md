# Fault-Tolerant Fleet Manager

A deliberately small Go distributed-systems project demonstrating how independent processes coordinate when communication is unreliable and processes can fail.

The fleet domain is intentionally simple: vehicles are independent processes moving on a 2D integer grid toward task destinations. The interesting part is **distributed coordination**, not fleet optimization.

## What this project demonstrates

- Independent Go processes communicating over MQTT pub/sub
- Goroutine-based concurrency
- Automatic heartbeats and failure detection
- Dropped, delayed, duplicated, and reordered messages
- Sequence numbers and stale-message handling
- Task reassignment after vehicle failure
- Controller persistence and restart/recovery
- Vehicle reconnection
- Deterministic failure/integration tests
- A minimal browser visualization

The guiding rule is:

> Every piece of complexity should exist because it demonstrates a concrete distributed-systems problem. If a feature does not help demonstrate one, leave it out.

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
                     │ persistence│ │           │
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
- stop if they fail to connect or receive messages from the broker

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
│   ├── domain/
│   │   └── types.go
│   │
│   ├── controller/
│   │   └── controller.go
│   │   └── http.go
│   │
│   ├── vehicle/
│   │   └── vehicle.go
│   │
│   │── mqtt/
│   │   └── client.go
│   │
│   │── fault/
│       └── injector.go
│   
├── web/
│   └── index.html
│
├── README.md
├── go.mod
├── go.sum
```

Do not create all of these directories before they are needed. Start with the smallest working system and add pieces incrementally.

---

# 3. Message protocol

Communication is JSON-encoded and sent over MQTT topics.

## Vehicle → Controller

**Heartbeat** (published every second)
- Topic: `heartbeat/{vehicle_id}`
- Payload: JSON with vehicle_id, position, sequence

## Controller → Vehicle

**AssignTask** (published when task is assigned)
- Topic: `task/{vehicle_id}`
- Payload: JSON with task_id, destination, generation

**Stop** (published when task is reassigned or vehicle fails)
- Topic: `stop/{vehicle_id}`
- Payload: JSON with reason

---

# 4. Message definitions (JSON)

All messages are JSON. Vehicles and controller encode/decode them.

## Heartbeat

Published by vehicle to `heartbeat/{vehicle_id}`:

```json
{
  "vehicle_id": "vehicle-1",
  "position": {
    "x": 5,
    "y": 3
  },
  "sequence": 42
}
```

## AssignTask

Published by controller to `task/{vehicle_id}`:

```json
{
  "task_id": "task-1",
  "destination": {
    "x": 10,
    "y": 5
  },
  "generation": 1
}
```

## Stop

Published by controller to `stop/{vehicle_id}`:

```json
{
  "reason": "task reassigned"
}
```

---

# 5. Message semantics

## Heartbeat

Vehicle publishes periodically:

```text
V1 → broker  heartbeat/vehicle-1
             {
               "vehicle_id": "vehicle-1",
               "position": {"x": 7, "y": 3},
               "sequence": 41
             }

Controller subscribes to heartbeat/+
Receives on callback
Extracts position, sequence
Updates authoritative state
Checks if task is complete
Records timestamp
```

The controller records the time at which the heartbeat was received.

Position updates are embedded in the heartbeat (no separate UpdatePosition message).

---

## AssignTask

When a vehicle is idle and a task is queued, the controller publishes:

```text
C → broker  task/vehicle-1
            {
              "task_id": "task-1",
              "destination": {"x": 10, "y": 5},
              "generation": 1
            }

V1 subscribes to task/vehicle-1
Receives on callback
Extracts task_id, destination, generation
Sets destination
Begins moving
```

Generation identifies the version of the assignment. If a task is later reassigned:

```text
C → broker  task/vehicle-2
            {
              "task_id": "task-1",
              "destination": {"x": 10, "y": 5},
              "generation": 2
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
              "reason": "task reassigned"
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

## Vehicle-side timeout

The vehicle also monitors its connection to the broker.

If it cannot receive any messages for its own timeout:

```text
V1:

broker connection lost
        ↓
STOP moving
```

This gives a useful safety property:

```text
             broker lost / partitioned
                   │
         ┌─────────┴─────────┐
         ▼                   ▼
   Controller             Vehicle
   can't hear V1          can't reach broker
         │                   │
         ▼                   ▼
   V1 OFFLINE             STOP
         │
         ▼
   task reassigned
```

This does **not** eliminate false positives. It makes communication loss fail safer.

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

The MQTT broker itself delivers reliably (QoS 1), but we can inject faults in callbacks:

```text
Vehicle publishes heartbeat
   │
   ▼
MQTT Broker (reliable delivery)
   │
   ▼
Controller callback
   │
   ├── DROP (ignore the message)
   ├── DELAY (process after N seconds)
   ├── DUPLICATE (process twice)
   └── REORDER (hold, process out of order)
   │
   ▼
Update state
```

Start with deterministic, one-shot faults.

Possible API:

```go
type FaultAction int

const (
    NoFault FaultAction = iota
    Drop
    Delay
    Duplicate
)

type FaultRule struct {
    MessageType string    // "heartbeat", "assigntask", "stop"
    VehicleID   string
    Sequence    uint64
    Action      FaultAction
    Delay       time.Duration
}
```

---

# 9. Persistence

No real database is necessary.

The controller persists its state to a local JSON file:

```text
state/state.json
```

Minimal interface:

```go
type Store interface {
    Load() (State, error)
    Save(State) error
}
```

The purpose is to demonstrate controller process recovery.

---

# 10. Scheduler

Keep scheduling simple.

A suitable initial implementation:

> Assign the task to the idle vehicle with the smallest Manhattan distance to the destination.

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

Do not build a generic optimization framework.

The scheduler is not the interesting part of the project.

---

# 11. Implementation order

Build incrementally.

### Sprint 1 — basic distributed system

1. Create Go module
2. Define domain types
3. Implement controller process
4. Implement vehicle process
5. Connect to shared MQTT broker
6. Implement heartbeat publish/subscribe
7. Implement task assignment publish/subscribe

Goal:

```text
Controller ←──── MQTT ────→ Vehicle

task assignment works
```

---

### Sprint 2 — movement

1. Implement 2D position
2. Implement vehicle movement
3. Move one grid cell at a time
4. Include position in heartbeat
5. Complete task when destination is reached

Goal:

```text
Controller → Vehicle  AssignTask

Vehicle moves:

(0,0)
(1,0)
(2,0)
...
(10,5)

Vehicle → Controller  Heartbeat
          ...

Controller:
    task completed
```

---

### Sprint 3 — liveness

1. Add automatic heartbeat goroutine
2. Record last heartbeat timestamp
3. Implement controller failure detector
4. Mark vehicles offline
5. Requeue their tasks
6. Implement vehicle-side broker connection timeout
7. Stop vehicle on connection loss

Goal:

```text
vehicle disconnects
       ↓
controller detects timeout
       ↓
task requeued
       ↓
another vehicle receives task
```

---

### Sprint 4 — message faults

1. Add deterministic fault injector
2. Implement message drop (in callback)
3. Implement delay (queue message, process later)
4. Implement duplication (process twice)
5. Implement reordering (hold/release)
6. Add sequence numbers
7. Reject stale/duplicate heartbeats

Goal:

The system continues to behave correctly despite unreliable delivery.

---

### Sprint 5 — recovery

1. Add local JSON persistence
2. Persist controller state
3. Simulate controller crash
4. Reload state on restart
5. Vehicles stay connected to broker during controller downtime
6. Reconcile vehicle and controller state
7. Handle stale assignments

Goal:

```text
controller crashes
       ↓
state persists to disk
       ↓
controller restarts
       ↓
loads state from disk
       ↓
vehicles continue heartbeating
       ↓
system resumes
```

---

### Sprint 6 — tests and visualization

1. Implement deterministic integration tests
2. Reproduce core scenarios
3. Add structured message logging
4. Build minimal HTML visualization
5. Display vehicles/tasks
6. Display online/offline state
7. Display event/message log

---

# 12. What not to build

Do not add complexity just because it sounds impressive.

Specifically avoid initially:

- graph-based route planning
- A*
- sophisticated optimization
- loading/unloading
- cargo management
- reservations
- fencing-token infrastructure
- distributed consensus
- Raft
- Kafka (MQTT is lighter-weight for this demo)
- Redis
- Kubernetes
- service meshes
- React
- TypeScript frontend
- real database
- cloud deployment
- authentication

The project should remain small enough that you can understand and explain essentially every important line.

---

# 13. Definition of done

The project is finished when you can reliably demonstrate:

```text
✓ Start controller
✓ Start multiple vehicles
✓ Create tasks
✓ Assign tasks via MQTT
✓ Vehicles move on a 2D grid
✓ Vehicles send automatic heartbeats
✓ Position updates embedded in heartbeats
✓ Tasks complete automatically at destination

✓ Drop messages
✓ Delay messages
✓ Duplicate messages
✓ Reorder messages

✓ Detect vehicle communication failure
✓ Requeue its task
✓ Reassign the task
✓ Prevent stale updates from corrupting state

✓ Crash controller
✓ Restart controller
✓ Reload persistent state
✓ Continue receiving heartbeats from vehicles
✓ Reconcile state

✓ Run deterministic integration tests
✓ See the system in a simple browser UI
✓ See the message/event log
```

If all of this works and you can explain **why every mechanism exists and what failure it handles**, stop.

Do not keep adding features simply to make the repository larger.
