package domain

import "time"

type Topic string

const (
	TopicHeartbeat Topic = "heartbeat"
	TopicTask      Topic = "task"
	TopicStop      Topic = "stop"
)

type Position struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type TaskStatus string

const (
	TaskQueued     TaskStatus = "QUEUED"
	TaskInProgress TaskStatus = "IN_PROGRESS"
	TaskCompleted  TaskStatus = "COMPLETED"
	TaskFailed     TaskStatus = "FAILED"
)

type Task struct {
	ID                   string     `json:"id"`
	Destination          Position   `json:"destination"`
	Status               TaskStatus `json:"status"`
	AssignedVehicle      string     `json:"assignedVehicle"`
	AssignmentGeneration int        `json:"assignmentGeneration"`
}

type VehicleStatus string

const (
	VehicleIdle    VehicleStatus = "IDLE"
	VehicleWorking VehicleStatus = "WORKING"
	VehicleOffline VehicleStatus = "OFFLINE"
)

type VehicleState struct {
	ID            string        `json:"id"`
	Position      Position      `json:"position"`
	CurrentTask   string        `json:"currentTask"`
	Status        VehicleStatus `json:"status"`
	LastHeartbeat time.Time     `json:"lastHeartbeat"`
	LastSequence  int           `json:"lastSequence"`
}

type HeartbeatMessage struct {
	VehicleID string   `json:"vehicleId"`
	Position  Position `json:"position"`
	Sequence  int      `json:"sequence"`
}

type AssignTaskMessage struct {
	TaskID      string   `json:"taskId"`
	Destination Position `json:"destination"`
	Generation  int      `json:"generation"`
}

type StopMessage struct {
	Reason string `json:"reason"`
}
