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
	StateClass  string // HA state_class; empty means "measurement"
}

// classRules maps substring patterns (matched against sensor name) to metadata.
// Rules are evaluated in order; first match wins.
var classRules = []struct {
	substr string
	meta   SensorMeta
}{
	{"temperature", SensorMeta{DeviceClass: "temperature", Unit: "°C"}},
	{"humidity", SensorMeta{DeviceClass: "humidity", Unit: "%"}},
	{"pressure", SensorMeta{DeviceClass: "pressure", Unit: "hPa"}},
	{"rainfall", SensorMeta{DeviceClass: "precipitation", Unit: "mm"}},
	{"rain", SensorMeta{DeviceClass: "precipitation", Unit: "mm"}},
	{"wind_speed", SensorMeta{DeviceClass: "wind_speed", Unit: "km/h"}},
	{"wind_direction", SensorMeta{DeviceClass: "wind_direction", Unit: "°", StateClass: "measurement_angle"}},
	{"solar_radiation", SensorMeta{DeviceClass: "irradiance", Unit: "W/m²"}},
	{"dissolved_oxygen", SensorMeta{Unit: "mg/L"}},
	{"sea_level", SensorMeta{DeviceClass: "distance", Unit: "m"}},
	{"tide", SensorMeta{DeviceClass: "distance", Unit: "m"}},
	{"salinity", SensorMeta{Unit: "PSU"}},
	{"specific_conductance", SensorMeta{Unit: "µS/cm"}},
	{"conductivity", SensorMeta{Unit: "µS/cm"}},
	{"turbidity", SensorMeta{Unit: "NTU"}},
	{"chlorophyll", SensorMeta{Unit: "µg/L"}},
	{"phycocyanin", SensorMeta{Unit: "RFU"}},
	{"battery", SensorMeta{DeviceClass: "voltage", Unit: "V"}},
	{"orp", SensorMeta{DeviceClass: "voltage", Unit: "mV"}},
	{"ph", SensorMeta{DeviceClass: "ph"}},
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

	// When a known device_class is set, HA validates the unit against it.
	// OCR sometimes garbles the live unit (e.g. "W/m²" → "wim2"), which would
	// fail validation, so trust the rule's unit and ignore the override.
	unit := meta.Unit
	if meta.DeviceClass == "" && unitOverride != "" {
		unit = unitOverride
	}

	stateClass := meta.StateClass
	if stateClass == "" {
		stateClass = "measurement"
	}

	uniqueID := "stri_bocas_" + sensorName
	stateTopic := topicPrefix + "/sensor/" + uniqueID + "/state"
	configTopic := topicPrefix + "/sensor/" + uniqueID + "/config"

	cfg := DiscoveryConfig{
		Name:              toTitle(sensorName),
		UniqueID:          uniqueID,
		StateTopic:        stateTopic,
		ValueTemplate:     "{{ value_json.value }}",
		UnitOfMeasurement: unit,
		DeviceClass:       meta.DeviceClass,
		StateClass:        stateClass,
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
