package homeassistant

import (
	"encoding/json"
	"strings"
)

// Device is the HA MQTT device block shared by all sensors.
var Device = map[string]any{
	"identifiers":  []string{"stri_bocas_del_toro"},
	"name":         "STRI Bocas del Toro",
	"manufacturer": "Smithsonian Tropical Research Institute",
	"model":        "Physical Monitoring Station",
}

// SensorMeta maps a normalised sensor name (snake_case) to HA device_class and unit.
type SensorMeta struct {
	DeviceClass string // HA device_class value ("temperature", "humidity", etc.) or "" for none
	Unit        string // unit_of_measurement shown in HA; may be overridden by live OCR unit
}

// classRules maps substring patterns (matched against sensor name) to metadata.
// Rules are evaluated in order; first match wins.
var classRules = []struct {
	substr string
	meta   SensorMeta
}{
	{"temperature", SensorMeta{"temperature", "°C"}},
	{"humidity", SensorMeta{"humidity", "%"}},
	{"pressure", SensorMeta{"pressure", "hPa"}},
	{"rainfall", SensorMeta{"precipitation", "mm"}},
	{"rain", SensorMeta{"precipitation", "mm"}},
	{"wind_speed", SensorMeta{"wind_speed", "m/s"}},
	{"wind_direction", SensorMeta{"wind_direction", "°"}},
	{"solar_radiation", SensorMeta{"irradiance", "W/m²"}},
	{"dissolved_oxygen", SensorMeta{"", "mg/L"}},
	{"sea_level", SensorMeta{"", "m"}},
	{"salinity", SensorMeta{"salinity", "PSU"}},
	{"conductivity", SensorMeta{"", "µS/cm"}},
	{"turbidity", SensorMeta{"", "NTU"}},
	{"chlorophyll", SensorMeta{"", "µg/L"}},
	{"orp", SensorMeta{"voltage", "mV"}},
	{"ph", SensorMeta{"ph", ""}},
}

// MetaFor returns the best SensorMeta for a snake_case sensor name.
func MetaFor(name string) SensorMeta {
	lower := strings.ToLower(name)
	for _, rule := range classRules {
		if strings.Contains(lower, rule.substr) {
			return rule.meta
		}
	}
	return SensorMeta{}
}

// DiscoveryConfig is the JSON payload published to the HA MQTT discovery topic.
type DiscoveryConfig struct {
	Name              string         `json:"name"`
	UniqueID          string         `json:"unique_id"`
	StateTopic        string         `json:"state_topic"`
	ValueTemplate     string         `json:"value_template"`
	UnitOfMeasurement string         `json:"unit_of_measurement,omitempty"`
	DeviceClass       string         `json:"device_class,omitempty"`
	StateClass        string         `json:"state_class"`
	Device            map[string]any `json:"device"`
}

// BuildDiscovery constructs the discovery config JSON for one sensor.
// sensorName is snake_case (e.g. "air_temperature").
// topicPrefix is typically "homeassistant".
// unitOverride may come from live OCR; empty string keeps the default.
func BuildDiscovery(sensorName, topicPrefix, unitOverride string) (string, []byte, error) {
	meta := MetaFor(sensorName)

	unit := meta.Unit
	if unitOverride != "" {
		unit = unitOverride
	}

	uniqueID := "stri_bocas_" + sensorName
	stateTopic := topicPrefix + "/sensor/" + uniqueID + "/state"
	configTopic := topicPrefix + "/sensor/" + uniqueID + "/config"

	cfg := DiscoveryConfig{
		Name:              "STRI Bocas " + toTitle(sensorName),
		UniqueID:          uniqueID,
		StateTopic:        stateTopic,
		ValueTemplate:     "{{ value_json.value }}",
		UnitOfMeasurement: unit,
		DeviceClass:       meta.DeviceClass,
		StateClass:        "measurement",
		Device:            Device,
	}

	payload, err := json.Marshal(cfg)
	if err != nil {
		return "", nil, err
	}
	return configTopic, payload, nil
}

// StateTopic returns the MQTT topic for sensor state updates.
func StateTopic(sensorName, topicPrefix string) string {
	return topicPrefix + "/sensor/stri_bocas_" + sensorName + "/state"
}

// toTitle converts snake_case to "Title Case" for display names.
func toTitle(s string) string {
	words := strings.Split(s, "_")
	for i, w := range words {
		if len(w) == 0 {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}
