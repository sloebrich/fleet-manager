package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"fleet-manager/src/config"
	"fleet-manager/src/controller"
	"fleet-manager/src/domain"
	mqtt "fleet-manager/src/mqtt"
)

func main() {
	brokerURL := flag.String("broker", "tcp://localhost:9090", "MQTT broker URL")
	scenarioPath := flag.String("scenario", "scenarios/scenario.json", "path to scenario file")
	flag.Parse()

	client, err := mqtt.New("controller", *brokerURL)
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

	scenario, err := config.LoadScenario(*scenarioPath)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("loaded %d tasks and %d faults", len(scenario.Tasks), len(scenario.Faults))

	for _, task := range scenario.Tasks {
		controller.CreateTask(task.ID, task.Destination)
	}

	for _, f := range scenario.Faults {
		log.Printf(
			"adding fault: action=%s vehicle=%s sequence=%d",
			f.Action,
			f.VehicleID,
			f.Sequence,
		)

		action, err := config.ParseFaultAction(f.Action)
		if err != nil {
			log.Fatal(err)
		}

		controller.AddFault(
			action,
			f.VehicleID,
			f.Sequence,
			0,
		)
	}

	select {}
}
