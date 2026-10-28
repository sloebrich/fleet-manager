package config

import (
	"encoding/json"
	"fleet-manager/src/domain"
	"fleet-manager/src/fault"
	"fmt"
	"os"
)

type Scenario struct {
	Tasks  []TaskConfig  `json:"tasks"`
	Faults []FaultConfig `json:"faults"`
}

type TaskConfig struct {
	ID          string          `json:"id"`
	Destination domain.Position `json:"destination"`
}

type FaultConfig struct {
	Action    string `json:"action"`
	VehicleID string `json:"vehicle_id"`
	Sequence  int    `json:"sequence"`
	DelayMS   int    `json:"delay_ms"`
}

func LoadScenario(path string) (Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, err
	}

	var scenario Scenario
	err = json.Unmarshal(data, &scenario)
	if err != nil {
		return Scenario{}, err
	}

	return scenario, nil
}

func ParseFaultAction(action string) (fault.Action, error) {
	switch action {
	case "drop":
		return fault.Drop, nil
	case "delay":
		return fault.Delay, nil
	case "duplicate":
		return fault.Duplicate, nil
	default:
		var zero fault.Action
		return zero, fmt.Errorf("unknown fault action %q", action)
	}
}
