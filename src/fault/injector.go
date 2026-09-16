package fault

import (
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

func matches(rule Rule, vehicleID string, sequence int) bool {
	if rule.VehicleID != "" && rule.VehicleID != vehicleID {
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

func (i *Injector) ShouldDrop(sequence int, vehicleID string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()

	for _, rule := range i.rules {
		if rule.Action == Drop && matches(rule, vehicleID, sequence) {
			return true
		}
	}
	return false
}

func (i *Injector) ShouldDelay(sequence int, vehicleID string) *time.Duration {
	i.mu.Lock()
	defer i.mu.Unlock()

	for _, rule := range i.rules {
		if rule.Action == Delay && matches(rule, vehicleID, sequence) {
			return &rule.Delay
		}
	}
	return nil
}

func (i *Injector) ShouldDuplicate(sequence int, vehicleID string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()

	for _, rule := range i.rules {
		if rule.Action == Duplicate && matches(rule, vehicleID, sequence) {
			return true
		}
	}
	return false
}
