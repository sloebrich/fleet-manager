package vehicle

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"fleet-manager/src/domain"
	"fleet-manager/src/fault"

	mqtt "fleet-manager/src/mqtt"

	paho "github.com/eclipse/paho.mqtt.golang"
)

type Vehicle struct {
	ID       string
	Position *domain.Position
	Sequence int

	client *mqtt.Client

	mu                sync.Mutex
	destination       *domain.Position
	currentTask       string
	currentGeneration int

	faultInjector *fault.Injector
}

func New(id string, position *domain.Position, client *mqtt.Client) *Vehicle {
	return &Vehicle{
		ID:            id,
		Position:      position,
		client:        client,
		faultInjector: fault.New(),
	}
}

func (v *Vehicle) Run() {
	for {
		v.move()
		v.heartbeat()
		time.Sleep(time.Second)
	}
}

func (c *Vehicle) AddFault(action fault.Action, sequence int, delay time.Duration) {
	sequencePtr := &sequence
	if sequence <= 0 {
		sequencePtr = nil
	}
	c.faultInjector.AddRule(fault.Rule{
		Action:    action,
		VehicleID: "",
		Sequence:  sequencePtr,
		Delay:     delay,
	})
}

func (v *Vehicle) publish(topic domain.Topic, sequence int, payload []byte) error {
	if v.faultInjector.ShouldDrop(sequence, "") {
		log.Printf(
			"FAULT DROP heartbeat vehicle=%s sequence=%d",
			v.ID,
			sequence,
		)
		return nil
	}
	if delay := v.faultInjector.ShouldDelay(sequence, ""); delay != nil {
		log.Printf(
			"FAULT DELAY heartbeat vehicle=%s sequence=%d",
			v.ID,
			sequence,
		)
		go func(seq int) {
			time.Sleep(*delay)
			log.Printf("heartbeat: vehicle=%s, position=(%d,%d) sequence=%d", v.ID, v.Position.X, v.Position.Y, seq)
			err := v.client.Publish(fmt.Sprintf("%s/%s", topic, v.ID), payload)
			if err != nil {
				log.Println("heartbeat failed:", err)
			}
		}(sequence)
		return nil
	}
	if v.faultInjector.ShouldDuplicate(sequence, "") {
		log.Printf(
			"FAULT DUPLICATE heartbeat vehicle=%s sequence=%d",
			v.ID,
			sequence,
		)
		log.Printf("heartbeat: vehicle=%s, position=(%d,%d) sequence=%d", v.ID, v.Position.X, v.Position.Y, v.Sequence)
		err := v.client.Publish(fmt.Sprintf("%s/%s", topic, v.ID), payload)
		if err != nil {
			log.Println("heartbeat failed:", err)
		}
	}
	log.Printf("heartbeat: vehicle=%s, position=(%d,%d) sequence=%d", v.ID, v.Position.X, v.Position.Y, v.Sequence)
	err := v.client.Publish(fmt.Sprintf("%s/%s", topic, v.ID), payload)
	if err != nil {
		log.Println("heartbeat failed:", err)
	}
	return err
}

func (v *Vehicle) heartbeat() error {
	v.Sequence++

	message := domain.HeartbeatMessage{
		VehicleID:   v.ID,
		Position:    *v.Position,
		Sequence:    v.Sequence,
		CurrentTask: v.currentTask,
	}

	payload, err := json.Marshal(message)
	if err != nil {
		log.Println("failed to marshal heartbeat:", err)
		return err
	}

	err = v.publish(domain.TopicHeartbeat, message.Sequence, payload)

	return err
}

func (v *Vehicle) move() {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.destination == nil {
		return
	}

	switch {
	case v.Position.X < v.destination.X:
		v.Position.X++
	case v.Position.X > v.destination.X:
		v.Position.X--
	case v.Position.Y < v.destination.Y:
		v.Position.Y++
	case v.Position.Y > v.destination.Y:
		v.Position.Y--
	}
}

func (v *Vehicle) HandleTask(client paho.Client, message paho.Message) {
	var task domain.AssignTaskMessage

	if err := json.Unmarshal(message.Payload(), &task); err != nil {
		log.Println("invalid task:", err)
		return
	}

	if task.Generation < v.currentGeneration {
		log.Printf("ignoring stale assignment gen=%d < current=%d", task.Generation, v.currentGeneration)
		return
	}

	v.currentTask = task.TaskID
	v.currentGeneration = task.Generation
	v.destination = &domain.Position{X: task.Destination.X, Y: task.Destination.Y}

	log.Printf(
		"task received: task=%s destination=(%d,%d) generation=%d",
		task.TaskID,
		task.Destination.X,
		task.Destination.Y,
		task.Generation,
	)
}

func (v *Vehicle) HandleStop(client paho.Client, message paho.Message) {
	var stopMsg domain.StopMessage

	if err := json.Unmarshal(message.Payload(), &stopMsg); err != nil {
		log.Println("invalid stop message:", err)
		return
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	v.destination = nil
	v.currentTask = ""
	v.currentGeneration = 0

	log.Printf("stopped: %s", stopMsg.Reason)
}
