package main

import (
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
var charts = []struct {
	URL  string
	Name string
}{
	{
		"https://biogeodb.stri.si.edu/physical_monitoring/images/charts/Bocas_24hrs.png",
		"bocas_24hrs",
	},
	{
		"https://biogeodb.stri.si.edu/physical_monitoring/images/charts/BocasExosonde24H-Set1.png",
		"exosonde_set1",
	},
	{
		"https://biogeodb.stri.si.edu/physical_monitoring/images/charts/BocasExosonde24H-Set2.png",
		"exosonde_set2",
	},
	{
		"https://biogeodb.stri.si.edu/physical_monitoring/images/charts/Bocas_sealevel24hrs.png",
		"sealevel_24hrs",
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

			readings, err := vision.ExtractReadings(imgBytes, chart.Name)
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
