package cmd

import "testing"

func TestSlackGatewayServeCommandIsRegistered(t *testing.T) {
	slackCommand, _, err := rootCmd.Find([]string{"slack", "gateway", "serve"})
	if err != nil {
		t.Fatalf("Find(slack gateway serve) error = %v", err)
	}
	if slackCommand == nil || slackCommand.Name() != "serve" {
		t.Fatalf("slack gateway serve command not found")
	}
}

func TestSlackMessageCommandsAreRegistered(t *testing.T) {
	for _, args := range [][]string{
		{"slack", "msg", "send"},
		{"slack", "msg", "history"},
		{"slack", "msg", "thread"},
		{"slack", "msg", "react"},
		{"slack", "msg", "react", "list"},
		{"slack", "msg", "react", "remove"},
	} {
		command, _, err := rootCmd.Find(args)
		if err != nil {
			t.Fatalf("Find(%v) error = %v", args, err)
		}
		if command == nil || command.Name() != args[len(args)-1] {
			t.Fatalf("%v command not found", args)
		}
	}
}
