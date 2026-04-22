package main

import (
	"image"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AMcPherran/stri-data-bridge/internal/config"
	"github.com/AMcPherran/stri-data-bridge/internal/fetcher"
	"github.com/AMcPherran/stri-data-bridge/internal/mqttclient"
	"github.com/AMcPherran/stri-data-bridge/internal/vision"
)

// charts lists every image URL and a human-friendly name used in logging and OCR context.
// Specs, when provided, drive ordered extraction so charts with duplicate units
// (e.g. two "celsius" sensors) are parsed correctly. Omit Specs to use the
// generic unit-lookup parser.
// Crops, when provided alongside Specs, bypass dynamic box detection entirely:
// each rectangle is cropped directly from the original image in pixel coordinates.
var charts = []struct {
	URL   string
	Name  string
	Specs []vision.SensorSpec
	Crops []image.Rectangle
}{
	{
		URL:  "https://biogeodb.stri.si.edu/physical_monitoring/images/charts/Bocas_24hrs.png",
		Name: "bocas_24hrs",
		// Sensors appear top-to-bottom in the Latest Value column.
		Specs: []vision.SensorSpec{
			{Name: "air_temperature", UnitKey: "celsius", Unit: "°C"},
			{Name: "relative_humidity", UnitKey: "%", Unit: "%"},
			{Name: "wind_speed", UnitKey: "km", Unit: "km/h"},
			{Name: "wind_direction", UnitKey: "deg", Unit: "°"},
			{Name: "water_temperature", UnitKey: "celsius", Unit: "°C"},
			{Name: "rainfall", UnitKey: "mm", Unit: "mm"},
			{Name: "solar_radiation_north", UnitKey: "w/m", Unit: "W/m²"},
			{Name: "solar_radiation_south", UnitKey: "w/m", Unit: "W/m²"},
		},
		// Hardcoded pixel rectangles for each value box in the original 1196×952
		// Bocas_24hrs.png. The STRI chart layout has been stable for years.
		// X range 808–904 spans the interior of the "Latest Value" column.
		// Y ranges align with each sensor's chart row, inset from the border lines.
		Crops: []image.Rectangle{
			image.Rect(808, 75, 904, 157),  // air_temperature
			image.Rect(808, 161, 904, 253), // relative_humidity
			image.Rect(808, 257, 904, 349), // wind_speed
			image.Rect(808, 353, 904, 445), // wind_direction
			image.Rect(808, 449, 904, 541), // water_temperature
			image.Rect(808, 545, 904, 637), // rainfall
			image.Rect(808, 647, 904, 793), // solar_radiation_north
			image.Rect(808, 797, 904, 943), // solar_radiation_south
		},
	},
	{
		URL:  "https://biogeodb.stri.si.edu/physical_monitoring/images/charts/BocasExosonde24H-Set1.png",
		Name: "exosonde_set1",
		// Sensors appear top-to-bottom in the Latest Value column (1198×1100).
		// X range 835–918 spans the interior of the value boxes in the right panel.
		Specs: []vision.SensorSpec{
			{Name: "sonde_water_temperature", UnitKey: "c", Unit: "°C"},
			{Name: "specific_conductance", UnitKey: "us/cm", Unit: "µS/cm"},
			{Name: "conductivity", UnitKey: "ms/cm", Unit: "mS/cm"},
			{Name: "total_dissolved_solids", UnitKey: "mg/l", Unit: "mg/L"},
			{Name: "salinity", UnitKey: "ppt", Unit: "PSU"},
			{Name: "dissolved_oxygen_pct", UnitKey: "%", Unit: "%"},
			{Name: "dissolved_oxygen", UnitKey: "", Unit: "mg/L"},
			{Name: "ph", UnitKey: "", Unit: "", MaxVal: 14},
			{Name: "sonde_battery_voltage", UnitKey: "volt", Unit: "V"},
		},
		Crops: []image.Rectangle{
			image.Rect(835, 78, 918, 156),   // water_temperature
			image.Rect(835, 189, 918, 267),  // specific_conductance
			image.Rect(835, 282, 918, 361),  // conductivity
			image.Rect(835, 395, 918, 476),  // total_dissolved_solids
			image.Rect(835, 490, 918, 569),  // salinity
			image.Rect(835, 617, 918, 697),  // dissolved_oxygen_pct
			image.Rect(835, 731, 918, 810),  // dissolved_oxygen
			image.Rect(835, 849, 918, 940),  // ph
			image.Rect(835, 981, 918, 1060), // battery_voltage
		},
	},
	{
		URL:  "https://biogeodb.stri.si.edu/physical_monitoring/images/charts/BocasExosonde24H-Set2.png",
		Name: "exosonde_set2",
		// Sensors appear top-to-bottom in the Latest Value column (1198×840).
		// Same X range as Set1 (identical right-panel layout).
		Specs: []vision.SensorSpec{
			{Name: "pressure", UnitKey: "psi", Unit: "psi"},
			{Name: "depth", UnitKey: "m", Unit: "m"},
			{Name: "turbidity", UnitKey: "fnu", Unit: "FNU"},
			{Name: "phycocyanin", UnitKey: "rfu", Unit: "RFU"},
			{Name: "chlorophyll_a", UnitKey: "mic", Unit: "µg/L"},
			{Name: "fdom_qsu", UnitKey: "qsu", Unit: "QSU"},
			{Name: "fdom_rfu", UnitKey: "rfu", Unit: "RFU"},
		},
		Crops: []image.Rectangle{
			image.Rect(835, 79, 918, 158),  // pressure
			image.Rect(835, 188, 918, 267), // depth
			image.Rect(835, 282, 918, 361), // turbidity
			image.Rect(835, 397, 918, 476), // phycocyanin
			image.Rect(835, 496, 918, 575), // chlorophyll_a
			image.Rect(835, 618, 918, 697), // fluorescence
			image.Rect(835, 713, 918, 792), // sensor_7
		},
	},
	{
		URL:  "https://biogeodb.stri.si.edu/physical_monitoring/images/charts/Bocas_sealevel24hrs.png",
		Name: "sealevel_24hrs",
		// Sensors appear top-to-bottom in the Latest Value column (1200×1051).
		// X range 810–912; the large chart at y≈330–607 has no value box and is skipped.
		Specs: []vision.SensorSpec{
			{Name: "tide_level", UnitKey: "m", Unit: "m"},
			{Name: "tide_offset", UnitKey: "m", Unit: "m"},
			{Name: "sealevel_battery_voltage", UnitKey: "volt", Unit: "V"},
		},
		Crops: []image.Rectangle{
			image.Rect(810, 90, 912, 199),   // tide_level
			image.Rect(810, 217, 912, 327),  // tide_offset
			image.Rect(810, 904, 912, 1014), // battery_voltage
		},
	},
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	mqttClient, err := mqttclient.New(cfg)
	if err != nil {
		log.Fatalf("MQTT error: %v", err)
	}
	defer mqttClient.Disconnect()

	// Build the set of sensor names this run will publish, then remove any
	// stale HA discovery entries left over from previous code versions.
	var knownSensors []string
	for _, chart := range charts {
		for _, spec := range chart.Specs {
			knownSensors = append(knownSensors, spec.Name)
		}
	}
	// Brief pause so the broker finishes evicting any previous session that
	// shared our client ID before we attempt the wildcard subscription.
	time.Sleep(3 * time.Second)
	mqttClient.PruneStaleDiscovery(knownSensors)

	f := fetcher.New()

	poll := func() {
		log.Println("[poll] starting")
		for _, chart := range charts {
			imgBytes, changed, err := f.Fetch(chart.URL)
			if err != nil {
				log.Printf("[poll] fetch error for %s: %v", chart.Name, err)
				continue
			}
			if !changed {
				log.Printf("[poll] %s unchanged, skipping OCR", chart.Name)
				continue
			}

			readings, err := vision.ExtractReadings(imgBytes, chart.Name, chart.Specs, chart.Crops)
			if err != nil {
				log.Printf("[poll] OCR error for %s: %v", chart.Name, err)
				continue
			}

			if len(readings) == 0 {
				log.Printf("[poll] no readings parsed from %s", chart.Name)
				continue
			}

			for _, r := range readings {
				mqttClient.PublishReading(r)
			}
		}
		log.Println("[poll] complete")
	}

	// Run immediately on startup, then on each tick.
	poll()

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			poll()
		case sig := <-sigs:
			log.Printf("received signal %v, shutting down", sig)
			return
		}
	}
}
