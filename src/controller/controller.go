package controller

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

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
}

func New(client *mqtt.Client) *Controller {
	controller := &Controller{
		client:        client,
		vehicles:      make(map[string]*domain.VehicleState),
		tasks:         make(map[string]*domain.Task),
		nextSequence:  make(map[string]int),
		faultInjector: fault.New(),
	}

	go controller.failureDetector()
	go controller.scheduler()

	return controller
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

func (c *Controller) nextMessageSequence(vehicleID string) int {
	c.nextSequence[vehicleID]++

	return c.nextSequence[vehicleID]
}

func (c *Controller) HandleHeartbeat(
	client paho.Client,
	message paho.Message) {
	var heartbeat domain.HeartbeatMessage

	if err := json.Unmarshal(message.Payload(), &heartbeat); err != nil {
		log.Println("invalid heartbeat:", err)
		return
	}

	log.Printf("received vehicle=%s, sequence=%d", heartbeat.VehicleID, heartbeat.Sequence)

	c.heartbeat(heartbeat)
}

func (c *Controller) heartbeat(
	req domain.HeartbeatMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()

	vehicle, exists := c.vehicles[req.VehicleID]

	if !exists {
		vehicle = &domain.VehicleState{
			ID:     req.VehicleID,
			Status: domain.VehicleIdle,
		}

		c.vehicles[req.VehicleID] = vehicle
	}

	if vehicle.Status == domain.VehicleOffline {
		vehicle.Status = domain.VehicleIdle
	}

	if req.Sequence > vehicle.LastSequence {
		vehicle.Position = domain.Position{X: req.Position.X, Y: req.Position.Y}
		vehicle.LastSequence = req.Sequence

		if task, ok := c.tasks[vehicle.CurrentTask]; ok && task.Status == domain.TaskInProgress {
			if vehicle.Position.X == task.Destination.X && vehicle.Position.Y == task.Destination.Y {
				task.Status = domain.TaskCompleted
				vehicle.Status = domain.VehicleIdle
				vehicle.CurrentTask = ""
				log.Printf("task %s completed by %s", task.ID, vehicle.ID)

			}
		}
	}

	vehicle.LastHeartbeat = time.Now()
}

func (c *Controller) publish(topic domain.Topic, sequence int, vehicleID string, payload []byte) error {
	if c.faultInjector.ShouldDrop(sequence, vehicleID) {
		log.Printf(
			"FAULT DROP heartbeat vehicle=%s sequence=%d",
			vehicleID,
			sequence,
		)
		return nil
	}
	if delay := c.faultInjector.ShouldDelay(sequence, vehicleID); delay != nil {
		log.Printf(
			"FAULT DELAY heartbeat vehicle=%s sequence=%d",
			vehicleID,
			sequence,
		)
		go func() {
			time.Sleep(*delay)
			err := c.client.Publish(fmt.Sprintf("%s/%s", topic, vehicleID), payload)
			if err != nil {
				log.Println("heartbeat failed:", err)
			}
		}()
		return nil
	}
	if c.faultInjector.ShouldDuplicate(sequence, "") {
		log.Printf(
			"FAULT DUPLICATE heartbeat vehicle=%s sequence=%d",
			vehicleID,
			sequence,
		)
		err := c.client.Publish(fmt.Sprintf("%s/%s", topic, vehicleID), payload)
		if err != nil {
			log.Println("heartbeat failed:", err)
		}

	}
	err := c.client.Publish(fmt.Sprintf("%s/%s", topic, vehicleID), payload)
	if err != nil {
		log.Println("heartbeat failed:", err)
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
		if vehicle.Status == domain.VehicleOffline {
			continue
		}

		if now.Sub(vehicle.LastHeartbeat) > heartbeatTimeout {
			vehicle.Status = domain.VehicleOffline

			assignedTask, ok := c.tasks[vehicle.CurrentTask]
			if ok {
				assignedTask.AssignedVehicle = ""
				assignedTask.Status = domain.TaskQueued
			}

			log.Printf(
				"vehicle offline: %s",
				vehicle.ID,
			)
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

	log.Printf("assigned task=%s to vehicle=%s generation=%d", task.ID, vehicle.ID, task.AssignmentGeneration)

	return err
}

func (c *Controller) stopVehicle(vehicleId string, reason string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

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

	return err
}
