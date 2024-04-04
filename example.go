package main

import (
	"context"
	"go.temporal.io/sdk/client"
	"log"
	"runner/activity/unit"
)

func main() {
	c, err := client.Dial(client.Options{})
	if err != nil {
		log.Fatalln("Unable to create client", err)
	}
	defer c.Close()

	workflowOptions := client.StartWorkflowOptions{
		TaskQueue: "unitman-runner-queue",
	}

	we, err := c.ExecuteWorkflow(context.Background(), workflowOptions, unit.UstanovitResultatSbrosaPodgotovkiUnitaWorkflow)
	if err != nil {
		log.Fatalln("Unable to execute workflow", err)
	}

	log.Println("Started workflow", "WorkflowID", we.GetID(), "RunID", we.GetRunID())
}
