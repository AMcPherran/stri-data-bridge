package config

import (
	"fmt"
	"os"
	"time"
)

// Config holds all runtime configuration sourced from environment variables.
type Config struct {
	MQTTBroker      string
	MQTTUsername    string
	MQTTPassword    string
	MQTTClientID    string
	PollInterval    time.Duration
	TopicPrefix     string
}

func Load() (*Config, error) {
	broker := os.Getenv("MQTT_BROKER")
	if broker == "" {
		return nil, fmt.Errorf("MQTT_BROKER is required")
	}

	clientID := os.Getenv("MQTT_CLIENT_ID")
	if clientID == "" {
		clientID = "stri-data-bridge"
	}

	topicPrefix := os.Getenv("MQTT_TOPIC_PREFIX")
	if topicPrefix == "" {
		topicPrefix = "homeassistant"
	}

	pollStr := os.Getenv("POLL_INTERVAL")
	if pollStr == "" {
		pollStr = "15m"
	}
	pollInterval, err := time.ParseDuration(pollStr)
	if err != nil {
		return nil, fmt.Errorf("invalid POLL_INTERVAL %q: %w", pollStr, err)
	}

	return &Config{
		MQTTBroker:   broker,
		MQTTUsername: os.Getenv("MQTT_USERNAME"),
		MQTTPassword: os.Getenv("MQTT_PASSWORD"),
		MQTTClientID: clientID,
		PollInterval: pollInterval,
		TopicPrefix:  topicPrefix,
	}, nil
}
