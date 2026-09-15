package main

import (
	"fmt"
	"log"

	"fleet-manager/src/domain"
	mqtt "fleet-manager/src/mqtt"
	"fleet-manager/src/vehicle"
)

func main() {
	vehicleId := "vehicle-1"
	client, err := mqtt.New(vehicleId, "tcp://localhost:1883")
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	v := vehicle.New(vehicleId, &domain.Position{X: 0, Y: 0}, client)

	err = client.Subscribe(fmt.Sprintf("%s/%s", domain.TopicTask, vehicleId), v.HandleTask)
	if err != nil {
		log.Fatal(err)
	}

	err = client.Subscribe(fmt.Sprintf("%s/%s", domain.TopicStop, vehicleId), v.HandleStop)
	if err != nil {
		log.Fatal(err)
	}

	v.Run()
}
