package controller

import (
	"encoding/json"
	"fleet-manager/src/domain"
	"net/http"
)

type StateResponse struct {
	Vehicles map[string]interface{} `json:"vehicles"`
	Tasks    map[string]interface{} `json:"tasks"`
}

type EventsResponse struct {
	Events []domain.Event `json:"events"`
}

func (c *Controller) HandleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	response := StateResponse{
		Vehicles: make(map[string]interface{}),
		Tasks:    make(map[string]interface{}),
	}

	for id, vehicle := range c.vehicles {
		response.Vehicles[id] = vehicle
	}

	for id, task := range c.tasks {
		response.Tasks[id] = task
	}

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "failed to encode state", http.StatusInternalServerError)
	}
}

func (c *Controller) HandleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	response := EventsResponse{
		Events: c.events.Snapshot(),
	}

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "failed to encode events", http.StatusInternalServerError)
	}
}
