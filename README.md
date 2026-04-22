# stri-data-bridge

A Go service that scrapes environmental sensor data from the [Smithsonian Tropical Research Institute](https://biogeodb.stri.si.edu/physical_monitoring/) physical monitoring station at Bocas del Toro, Panama and publishes it to Home Assistant via MQTT Discovery.

The STRI monitoring site only publishes data as PNG chart images. This bridge downloads those images on a configurable schedule, extracts the latest sensor values using Tesseract OCR, and publishes them as properly-typed Home Assistant entities — complete with device classes, units, and automatic cleanup of stale entities.

## Sensors

### Atmospheric (`Bocas_24hrs.png`)
| Sensor | Unit |
|---|---|
| Air Temperature | °C |
| Relative Humidity | % |
| Wind Speed | km/h |
| Wind Direction | ° |
| Water Temperature | °C |
| Rainfall | mm |
| Solar Radiation (North) | W/m² |
| Solar Radiation (South) | W/m² |

### EXOsonde Water Quality — Set 1 (`BocasExosonde24H-Set1.png`)
| Sensor | Unit |
|---|---|
| Sonde Water Temperature | °C |
| Specific Conductance | µS/cm |
| Conductivity | mS/cm |
| Total Dissolved Solids | mg/L |
| Salinity | PSU |
| Dissolved Oxygen | % |
| Dissolved Oxygen | mg/L |
| pH | — |
| Sonde Battery Voltage | V |

### EXOsonde Water Quality — Set 2 (`BocasExosonde24H-Set2.png`)
| Sensor | Unit |
|---|---|
| Pressure | psi |
| Depth | m |
| Turbidity | FNU |
| Phycocyanin | RFU |
| Chlorophyll-a | µg/L |
| fDOM | QSU |
| fDOM | RFU |

### Sea Level (`Bocas_sealevel24hrs.png`)
| Sensor | Unit |
|---|---|
| Tide Level | m |
| Tide Offset | m |
| Battery Voltage | V |

## Requirements

- Docker and Docker Compose
- An MQTT broker reachable from the container (e.g. Mosquitto)
- Home Assistant with the MQTT integration enabled

## Quick Start

1. Copy the example environment file and fill in your broker details:

```
MQTT_BROKER=tcp://192.168.1.10:1883
MQTT_USERNAME=
MQTT_PASSWORD=
```

2. Start the service:

```bash
docker compose up -d
```

Sensors will appear in Home Assistant under the **STRI Bocas del Toro** device within one poll cycle.

## Configuration

All configuration is via environment variables.

| Variable | Required | Default | Description |
|---|---|---|---|
| `MQTT_BROKER` | Yes | — | Broker URL, e.g. `tcp://192.168.1.1:1883` |
| `MQTT_USERNAME` | No | — | MQTT username |
| `MQTT_PASSWORD` | No | — | MQTT password |
| `MQTT_CLIENT_ID` | No | `stri-data-bridge` | MQTT client identifier |
| `MQTT_TOPIC_PREFIX` | No | `homeassistant` | Root topic for MQTT Discovery |
| `POLL_INTERVAL` | No | `15m` | How often to fetch and OCR the charts (Go duration syntax, e.g. `5m`, `1h`) |
| `STRI_DEBUG_IMAGES` | No | — | Set to any non-empty value to save OCR debug images to `/debug/` |

## Debug Images

When `STRI_DEBUG_IMAGES=1` is set, the service writes two kinds of images to `/debug/` (mounted to `./debug-images/` by default in `docker-compose.yml`):

- `stri-debug-<chart>.png` — the full binarized sidebar crop fed to Tesseract for unstructured charts
- `stri-debug-<chart>-box<N>-<sensor>.png` — the individual preprocessed crop for each sensor value box, showing exactly what Tesseract receives

These are useful for diagnosing OCR failures or verifying crop coordinates after the STRI chart layout changes.

## How It Works

1. **Fetch** — each chart PNG is downloaded with ETag/Last-Modified caching; unchanged images are skipped
2. **OCR** — each sensor's value box is cropped from the original image at hardcoded pixel coordinates, then contrast-enhanced, binarized, and 2× scaled before being passed to Tesseract with `PSM_SINGLE_LINE`
3. **Publish** — readings are published to MQTT as retained JSON state messages; Home Assistant Discovery config is published once per sensor on first reading
4. **Prune** — on startup, any retained Discovery entries left over from previous sensor names are tombstoned by publishing an empty retained payload to their config topics

## Project Structure

```
main.go                          Chart definitions and poll loop
internal/
  config/config.go               Environment variable loading
  fetcher/fetcher.go             HTTP fetcher with ETag caching
  vision/vision.go               OCR pipeline and value parsing
  homeassistant/discovery.go     MQTT Discovery payload builder
  mqttclient/client.go           MQTT publish helpers and prune logic
```
