package fault

import (
	"fleet-manager/src/domain"
	"sync"
	"time"
)

type Action string

const (
	NoFault   Action = "NO_FAULT"
	Drop      Action = "DROP"
	Delay     Action = "DELAY"
	Duplicate Action = "DUPLICATE"
)

type Rule struct {
	Topic     domain.Topic
	VehicleID string
	Sequence  *int
	Action    Action
	Delay     time.Duration
}

type Injector struct {
	rules []Rule
	mu    sync.Mutex
}

func New() *Injector {
	return &Injector{}
}

func matches(rule Rule, topic domain.Topic, vehicleID string, sequence int) bool {
	if rule.Topic != topic {
		return false
	}
	if rule.VehicleID != vehicleID {
		return false
	}
	if rule.Sequence != nil && *rule.Sequence != sequence {
		return false
	}
	return true
}

func (i *Injector) AddRule(rule Rule) {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.rules = append(i.rules, rule)
}

func (i *Injector) ShouldDrop(topic domain.Topic, vehicleID string, sequence int) bool {
	i.mu.Lock()
	defer i.mu.Unlock()

	for _, rule := range i.rules {
		if rule.Action == Drop && matches(rule, topic, vehicleID, sequence) {
			return true
		}
	}
	return false
}

func (i *Injector) ShouldDelay(topic domain.Topic, vehicleID string, sequence int) *time.Duration {
	i.mu.Lock()
	defer i.mu.Unlock()

	for _, rule := range i.rules {
		if rule.Action == Delay && matches(rule, topic, vehicleID, sequence) {
			return &rule.Delay
		}
	}
	return nil
}

func (i *Injector) ShouldDuplicate(topic domain.Topic, vehicleID string, sequence int) bool {
	i.mu.Lock()
	defer i.mu.Unlock()

	for _, rule := range i.rules {
		if rule.Action == Duplicate && matches(rule, topic, vehicleID, sequence) {
			return true
		}
	}
	return false
}
