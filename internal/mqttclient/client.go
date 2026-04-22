package mqttclient

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/AMcPherran/stri-data-bridge/internal/config"
	"github.com/AMcPherran/stri-data-bridge/internal/homeassistant"
	"github.com/AMcPherran/stri-data-bridge/internal/vision"
)

// StatePayload is the JSON published to each sensor's state topic.
type StatePayload struct {
	Value     float64 `json:"value"`
	Unit      string  `json:"unit,omitempty"`
	Timestamp string  `json:"timestamp"`
}

// Client wraps the Paho MQTT client with HA-aware publish helpers.
type Client struct {
	inner       mqtt.Client
	topicPrefix string
	// discoveredSensors tracks sensors for which discovery has already been published.
	discoveredSensors map[string]bool
}

func New(cfg *config.Config) (*Client, error) {
	opts := mqtt.NewClientOptions().
		AddBroker(cfg.MQTTBroker).
		SetClientID(cfg.MQTTClientID).
		SetUsername(cfg.MQTTUsername).
		SetPassword(cfg.MQTTPassword).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(10 * time.Second).
		SetConnectionLostHandler(func(_ mqtt.Client, err error) {
			log.Printf("[mqtt] connection lost: %v", err)
		}).
		SetOnConnectHandler(func(_ mqtt.Client) {
			log.Println("[mqtt] connected")
		})

	inner := mqtt.NewClient(opts)
	if token := inner.Connect(); token.Wait() && token.Error() != nil {
		return nil, fmt.Errorf("connecting to MQTT broker: %w", token.Error())
	}

	return &Client{
		inner:             inner,
		topicPrefix:       cfg.TopicPrefix,
		discoveredSensors: make(map[string]bool),
	}, nil
}

// PublishReading publishes HA discovery (once per sensor) and a state update.
func (c *Client) PublishReading(r vision.SensorReading) {
	// Publish discovery config once per sensor name.
	if !c.discoveredSensors[r.Name] {
		topic, payload, err := homeassistant.BuildDiscovery(r.Name, c.topicPrefix, r.Unit)
		if err != nil {
			log.Printf("[mqtt] building discovery for %s: %v", r.Name, err)
			return
		}
		if token := c.inner.Publish(topic, 0, true, payload); token.Wait() && token.Error() != nil {
			log.Printf("[mqtt] publishing discovery for %s: %v", r.Name, token.Error())
			return
		}
		log.Printf("[mqtt] discovery published: %s", topic)
		c.discoveredSensors[r.Name] = true
	}

	// Publish state.
	stateTopic := homeassistant.StateTopic(r.Name, c.topicPrefix)
	state := StatePayload{
		Value:     r.Value,
		Unit:      r.Unit,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	payload, err := json.Marshal(state)
	if err != nil {
		log.Printf("[mqtt] marshaling state for %s: %v", r.Name, err)
		return
	}
	if token := c.inner.Publish(stateTopic, 0, true, payload); token.Wait() && token.Error() != nil {
		log.Printf("[mqtt] publishing state for %s: %v", r.Name, token.Error())
		return
	}
	log.Printf("[mqtt] state published: %s = %.4g %s", r.Name, r.Value, r.Unit)
}

// PruneStaleDiscovery removes HA MQTT discovery entries for sensors that are no
// longer published by this bridge. It subscribes to the wildcard config topic,
// waits briefly for all retained messages to arrive, then publishes an empty
// retained payload (the MQTT discovery "delete" signal) to any topic whose
// unique_id is not in knownSensorNames.
func (c *Client) PruneStaleDiscovery(knownSensorNames []string) {
	valid := make(map[string]bool, len(knownSensorNames))
	for _, name := range knownSensorNames {
		valid["stri_bocas_"+name] = true
	}

	wildcard := c.topicPrefix + "/sensor/stri_bocas_+/config"

	// Protect foundUIDs with a mutex: Paho dispatches message callbacks in its
	// own goroutines, so concurrent writes are possible.
	var mu sync.Mutex
	foundUIDs := make(map[string]string) // unique_id → config topic

	handler := func(_ mqtt.Client, msg mqtt.Message) {
		if len(msg.Payload()) == 0 {
			return // already tombstoned
		}
		var cfg struct {
			UniqueID string `json:"unique_id"`
		}
		if err := json.Unmarshal(msg.Payload(), &cfg); err == nil && cfg.UniqueID != "" {
			mu.Lock()
			foundUIDs[cfg.UniqueID] = msg.Topic()
			mu.Unlock()
		}
	}

	log.Printf("[mqtt] prune: subscribing to %s", wildcard)
	if token := c.inner.Subscribe(wildcard, 0, handler); token.Wait() && token.Error() != nil {
		log.Printf("[mqtt] prune: subscribe error: %v", token.Error())
		return
	}
	// Retained messages are delivered immediately on subscribe; 2 s is ample
	// even on slow brokers. We wait fully before reading foundUIDs.
	time.Sleep(2 * time.Second)
	c.inner.Unsubscribe(wildcard)
	// Brief pause so any in-flight handler goroutines finish writing to foundUIDs.
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	log.Printf("[mqtt] prune: found %d retained discovery config(s)", len(foundUIDs))
	for uid, topic := range foundUIDs {
		if valid[uid] {
			log.Printf("[mqtt] prune: keeping %s", uid)
			continue
		}
		log.Printf("[mqtt] prune: deleting stale %s (topic: %s)", uid, topic)
		if token := c.inner.Publish(topic, 0, true, []byte{}); token.Wait() && token.Error() != nil {
			log.Printf("[mqtt] prune: failed to delete %s: %v", topic, token.Error())
		}
	}
}

// Disconnect cleanly closes the MQTT connection.
func (c *Client) Disconnect() {
	c.inner.Disconnect(250)
}
