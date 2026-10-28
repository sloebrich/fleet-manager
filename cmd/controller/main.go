package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"fleet-manager/src/config"
	"fleet-manager/src/controller"
	"fleet-manager/src/domain"
	"fleet-manager/src/fault"
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

	log.Printf("loaded %d tasks and %d controller faults", len(scenario.Tasks), len(scenario.ControllerFaults))

	for _, task := range scenario.Tasks {
		controller.CreateTask(task.ID, task.Destination)
	}

	for _, f := range scenario.ControllerFaults {
		log.Printf(
			"adding fault: action=%s vehicle=%s sequence=%d delay_ms=%d",
			f.Action,
			f.VehicleID,
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

		controller.AddFault(
			action,
			f.VehicleID,
			f.Sequence,
			delay,
		)
	}

	select {}
}
