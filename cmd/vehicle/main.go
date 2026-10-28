package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"fleet-manager/src/config"
	"fleet-manager/src/domain"
	"fleet-manager/src/fault"
	mqtt "fleet-manager/src/mqtt"
	"fleet-manager/src/vehicle"
)

func main() {
	vehicleId := flag.String("id", "", "Unique identifier for the vehicle (required)")
	startX := flag.Int("startX", 0, "Starting X position of the vehicle")
	startY := flag.Int("startY", 0, "Starting Y position of the vehicle")
	brokerURL := flag.String("broker", "tcp://localhost:9090", "MQTT broker URL")
	scenarioPath := flag.String("scenario", "scenarios/scenario.json", "path to scenario file")
	flag.Parse()

	if *vehicleId == "" {
		fmt.Fprintln(os.Stderr, "Vehicle ID is required. Use -id flag to specify it.")
		os.Exit(1)
	}

	client, err := mqtt.New(*vehicleId, *brokerURL)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	scenario, err := config.LoadScenario(*scenarioPath)
	if err != nil {
		log.Fatal(err)
	}

	startingPosition := &domain.Position{
		X: *startX,
		Y: *startY,
	}

	for _, vConf := range scenario.Vehicles {
		if vConf.ID == *vehicleId {
			startingPosition.X = vConf.StartX
			startingPosition.Y = vConf.StartY
			break
		}
	}

	v := vehicle.New(*vehicleId, startingPosition, client)

	for _, f := range scenario.VehicleFaults {
		if f.VehicleID != "" && f.VehicleID != *vehicleId {
			continue
		}

		log.Printf(
			"adding vehicle fault: action=%s sequence=%d delay_ms=%d",
			f.Action,
			f.Sequence,
			f.DelayMS,
		)

		action, err := config.ParseFaultAction(f.Action)
		if err != nil {
			log.Fatal(err)
		}

		delay := time.Duration(f.DelayMS) * time.Millisecond
		if action == fault.Delay && delay <= 0 {
			log.Fatalf("delay fault for vehicle=%q sequence=%d needs delay_ms > 0", f.VehicleID, f.Sequence)
		}

		v.AddFault(action, f.Sequence, delay)
	}

	err = client.Subscribe(fmt.Sprintf("%s/%s", domain.TopicTask, *vehicleId), v.HandleTask)
	if err != nil {
		log.Fatal(err)
	}

	err = client.Subscribe(fmt.Sprintf("%s/%s", domain.TopicStop, *vehicleId), v.HandleStop)
	if err != nil {
		log.Fatal(err)
	}

	v.Run()
}
