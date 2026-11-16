package controller

import (
	"fleet-manager/src/domain"
	"sync"
	"time"
)

type eventLog struct {
	mu     sync.Mutex
	events []domain.Event
	nextID int
	max    int
}

func newEventLog(max int) *eventLog {
	return &eventLog{
		max:    max,
		events: make([]domain.Event, 0),
	}
}

func (l *eventLog) Add(eventType domain.EventType, message string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.nextID++
	l.events = append(l.events, domain.Event{
		ID:      l.nextID,
		Time:    time.Now(),
		Type:    eventType,
		Message: message,
	})

	for len(l.events) > l.max {
		l.events = l.events[1:]
	}

}

func (l *eventLog) Snapshot() []domain.Event {
	l.mu.Lock()
	defer l.mu.Unlock()

	snapshot := make([]domain.Event, len(l.events))
	copy(snapshot, l.events)

	return snapshot
}
