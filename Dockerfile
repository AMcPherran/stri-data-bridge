FROM golang:1.23-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    libtesseract-dev \
    libleptonica-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o stri-data-bridge .


FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    tesseract-ocr \
    tesseract-ocr-eng \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /app/stri-data-bridge /usr/local/bin/stri-data-bridge

# All configuration is via environment variables:
#   MQTT_BROKER      (required) e.g. tcp://192.168.1.1:1883
#   MQTT_USERNAME    (optional)
#   MQTT_PASSWORD    (optional)
#   MQTT_CLIENT_ID   (optional, default: stri-data-bridge)
#   POLL_INTERVAL    (optional, default: 15m)
#   MQTT_TOPIC_PREFIX (optional, default: homeassistant)

CMD ["stri-data-bridge"]
