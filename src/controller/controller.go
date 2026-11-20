package controller

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"fleet-manager/src/config"
	"fleet-manager/src/domain"
	"fleet-manager/src/fault"
	mqtt "fleet-manager/src/mqtt"

	paho "github.com/eclipse/paho.mqtt.golang"
)

const heartbeatTimeout = 3 * time.Second

type Controller struct {
	client *mqtt.Client

	mu       sync.Mutex
	vehicles map[string]*domain.VehicleState
	tasks    map[string]*domain.Task

	nextSequence map[string]int

	faultInjector *fault.Injector

	events *eventLog
}

func New(client *mqtt.Client) *Controller {
	controller := &Controller{
		client:        client,
		vehicles:      make(map[string]*domain.VehicleState),
		tasks:         make(map[string]*domain.Task),
		nextSequence:  make(map[string]int),
		faultInjector: fault.New(),
		events:        newEventLog(500),
	}

	go controller.failureDetector()
	go controller.scheduler()

	return controller
}

func (c *Controller) log(eventType domain.EventType, message string) {
	c.events.Add(eventType, message)
	log.Print(message)
}

func (c *Controller) AddFault(action fault.Action, vehicleID string, sequence int, delay time.Duration) {
	sequencePtr := &sequence
	if sequence <= 0 {
		sequencePtr = nil
	}
	c.faultInjector.AddRule(fault.Rule{
		Action:    action,
		VehicleID: vehicleID,
		Sequence:  sequencePtr,
		Delay:     delay,
	})
}

func (c *Controller) AddVehicles(vehicles []config.VehicleConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, v := range vehicles {
		c.vehicles[v.ID] = &domain.VehicleState{
			ID:     v.ID,
			Status: domain.VehicleNotReady,
		}
	}
}

func (c *Controller) nextMessageSequence(vehicleID string) int {
	c.nextSequence[vehicleID]++

	return c.nextSequence[vehicleID]
}

func (c *Controller) HandleHeartbeat(
	client paho.Client,
	message paho.Message) {
	var heartbeat domain.HeartbeatMessage

	if err := json.Unmarshal(message.Payload(), &heartbeat); err != nil {
		c.log(domain.EventTypeError, fmt.Sprintf("invalid heartbeat: %v", err))
		return
	}

	c.log(domain.EventTypeHeartbeat, fmt.Sprintf("received heartbeat from vehicle %s, sequence=%d", heartbeat.VehicleID, heartbeat.Sequence))

	if heartbeat.Sequence > 0 {
		c.heartbeat(heartbeat)
		return
	}

	c.mu.Lock()

	vehicle, exists := c.vehicles[heartbeat.VehicleID]
	if !exists {
		c.log(domain.EventTypeError, fmt.Sprintf("heartbeat from unknown vehicle %s", heartbeat.VehicleID))
		c.mu.Unlock()
		return
	}

	vehicle.Status = domain.VehicleIdle

	for _, vehicle := range c.vehicles {
		if vehicle.Status == domain.VehicleNotReady {
			c.mu.Unlock()
			return
		}
	}

	c.mu.Unlock()
	c.log(domain.EventTypeMessage, "All vehicles started")

	err := c.client.Publish(string(domain.TopicStart), []byte("{}"), true)
	if err != nil {
		c.log(domain.EventTypeError, fmt.Sprintf("could not send start message: %v", err))
	}
}

func (c *Controller) heartbeat(
	req domain.HeartbeatMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()

	vehicle, exists := c.vehicles[req.VehicleID]

	if !exists {
		c.log(domain.EventTypeError, fmt.Sprintf("could not find vehicle %s", req.VehicleID))
		return
	}

	if vehicle.Status == domain.VehicleOffline {
		vehicle.Status = domain.VehicleIdle
	}

	if req.Sequence > vehicle.LastSequence {
		vehicle.Position = domain.Position{X: req.Position.X, Y: req.Position.Y}
		vehicle.LastSequence = req.Sequence

		if task, ok := c.tasks[vehicle.CurrentTask]; ok && task.Status == domain.TaskInProgress {
			if req.CurrentTask != vehicle.CurrentTask {
				task.Status = domain.TaskQueued
				task.AssignedVehicle = ""
				if req.CurrentTask != "" {
					c.stopVehicle(req.VehicleID, fmt.Sprintf("Vehicle was executing task %s, but task %s assigned in the controller ", req.CurrentTask, vehicle.CurrentTask))
				}
				vehicle.CurrentTask = ""
				vehicle.Status = domain.VehicleIdle

			} else if vehicle.Position.X == task.Destination.X && vehicle.Position.Y == task.Destination.Y {
				task.Status = domain.TaskCompleted
				vehicle.Status = domain.VehicleIdle
				vehicle.CurrentTask = ""
				c.log(domain.EventTypeMessage, fmt.Sprintf("task %s completed by %s", task.ID, vehicle.ID))
			}
		}

	}

	vehicle.LastHeartbeat = time.Now()
}

func (c *Controller) publish(topic domain.Topic, sequence int, vehicleID string, payload []byte) error {
	if c.faultInjector.ShouldDrop(sequence, vehicleID) {
		c.log(domain.EventTypeMessage, fmt.Sprintf(
			"FAULT: Dropping message for vehicle=%s with sequence=%d ",
			vehicleID,
			sequence,
		))
		return nil
	}
	if delay := c.faultInjector.ShouldDelay(sequence, vehicleID); delay != nil {
		c.log(domain.EventTypeMessage, fmt.Sprintf(
			"FAULT: Delaying message for vehicle=%s with sequence=%d by %dms",
			vehicleID,
			sequence,
			*delay,
		))
		go func() {
			time.Sleep(*delay)
			err := c.client.Publish(fmt.Sprintf("%s/%s", topic, vehicleID), payload)
			if err != nil {
				c.log(domain.EventTypeError, fmt.Sprintf("publish failed: %v", err))
			}
		}()
		return nil
	}
	if c.faultInjector.ShouldDuplicate(sequence, vehicleID) {
		c.log(domain.EventTypeMessage, fmt.Sprintf(
			"FAULT: DUPLICATING message for vehicle=%s with sequence=%d ",
			vehicleID,
			sequence,
		))
		err := c.client.Publish(fmt.Sprintf("%s/%s", topic, vehicleID), payload)
		if err != nil {
			c.log(domain.EventTypeError, fmt.Sprintf("publish failed: %v", err))
		}

	}
	err := c.client.Publish(fmt.Sprintf("%s/%s", topic, vehicleID), payload)
	if err != nil {
		c.log(domain.EventTypeError, fmt.Sprintf("publish failed: %v", err))
	}
	return err
}

func (c *Controller) failureDetector() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		c.checkVehicles()
	}
}

func (c *Controller) checkVehicles() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()

	for _, vehicle := range c.vehicles {
		if vehicle.Status == domain.VehicleOffline || vehicle.Status == domain.VehicleNotReady {
			continue
		}

		if now.Sub(vehicle.LastHeartbeat) > heartbeatTimeout {
			vehicle.Status = domain.VehicleOffline

			assignedTask, ok := c.tasks[vehicle.CurrentTask]
			if ok {
				assignedTask.AssignedVehicle = ""
				assignedTask.Status = domain.TaskQueued
			}

			c.log(domain.EventTypeMessage, fmt.Sprintf(
				"vehicle offline: %s",
				vehicle.ID,
			))
		}
	}
}

func (c *Controller) CreateTask(id string, goal domain.Position) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.tasks[id] = &domain.Task{
		ID:          id,
		Destination: goal,
		Status:      domain.TaskQueued,
	}
}

func (c *Controller) scheduler() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		c.assignTasks()
	}
}

func (c *Controller) assignTasks() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, task := range c.tasks {
		if task.Status != domain.TaskQueued {
			continue
		}

		var bestVehicle *domain.VehicleState
		var bestDist int32 = 100_000
		for _, v := range c.vehicles {
			if v.Status != domain.VehicleIdle {
				continue
			}
			distToGoal := math.Abs(float64(v.Position.X)-float64(task.Destination.X)) + math.Abs(float64(v.Position.Y)-float64(task.Destination.Y))
			if distToGoal < float64(bestDist) {
				bestVehicle, bestDist = v, int32(distToGoal)
			}
		}

		if bestVehicle == nil {
			continue
		}

		c.assignTask(task, bestVehicle)
	}
}

func (c *Controller) assignTask(task *domain.Task, vehicle *domain.VehicleState) error {
	task.AssignedVehicle = vehicle.ID
	task.Status = domain.TaskInProgress
	task.AssignmentGeneration++

	vehicle.Status = domain.VehicleWorking
	vehicle.CurrentTask = task.ID

	sequence := c.nextMessageSequence(vehicle.ID)

	message := domain.AssignTaskMessage{
		TaskID:      task.ID,
		Destination: task.Destination,
		Generation:  task.AssignmentGeneration,
		Sequence:    sequence,
	}

	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}

	err = c.publish(domain.TopicTask, sequence, vehicle.ID, payload)

	c.log(domain.EventTypeMessage, fmt.Sprintf("assigned task=%s to vehicle=%s generation=%d", task.ID, vehicle.ID, task.AssignmentGeneration))
	return err
}

func (c *Controller) stopVehicle(vehicleId string, reason string) error {
	sequence := c.nextMessageSequence(vehicleId)

	message := domain.StopMessage{
		Reason:   reason,
		Sequence: sequence,
	}

	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}

	err = c.publish(domain.TopicStop, sequence, vehicleId, payload)
	c.log(domain.EventTypeMessage, fmt.Sprintf("stopped vehicle=%s reason=%s sequence=%d", vehicleId, reason, sequence))

	return err
}
