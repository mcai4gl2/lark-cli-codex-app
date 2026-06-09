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
