package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"fleet-manager/src/controller"
	"fleet-manager/src/domain"
	"fleet-manager/src/fault"
	mqtt "fleet-manager/src/mqtt"
)

func main() {
	client, err := mqtt.New("controller", "tcp://localhost:1883")
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	controller := controller.New(client)

	http.HandleFunc("/api/state", controller.HandleState)

	go func() {
		log.Println("HTTP server listening on :8080")

		if err := http.ListenAndServe(":8080", nil); err != nil {
			log.Fatal(err)
		}
	}()

	err = client.Subscribe(fmt.Sprintf("%s/+", domain.TopicHeartbeat), controller.HandleHeartbeat)
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Controller running")

	controller.CreateTask("task-1", domain.Position{X: 10, Y: 5})
	controller.CreateTask("task-2", domain.Position{X: 15, Y: 15})
	controller.CreateTask("task-3", domain.Position{X: 20, Y: 10})

	controller.AddFault(fault.Delay, "v1", 1, 5*time.Second)
	controller.AddFault(fault.Duplicate, "v1", 2, 0)

	select {}
}
