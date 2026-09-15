package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"fleet-manager/src/domain"
	mqtt "fleet-manager/src/mqtt"
	"fleet-manager/src/vehicle"
)

func main() {
	vehicleId := flag.String("id", "", "Unique identifier for the vehicle (required)")
	startX := flag.Int("startX", 0, "Starting X position of the vehicle")
	startY := flag.Int("startY", 0, "Starting Y position of the vehicle")
	flag.Parse()

	if *vehicleId == "" {
		fmt.Fprintln(os.Stderr, "Vehicle ID is required. Use -id flag to specify it.")
		os.Exit(1)
	}

	client, err := mqtt.New(*vehicleId, "tcp://localhost:1883")
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	v := vehicle.New(*vehicleId, &domain.Position{X: *startX, Y: *startY}, client)

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
