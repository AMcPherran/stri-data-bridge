# stri-data-bridge

Go service that scrapes the Smithsonian Tropical Research Institute (STRI) physical-monitoring station at Bocas del Toro, Panama. STRI publishes data only as PNG chart images, so the bridge downloads the charts on a schedule, OCRs the latest values with Tesseract, and publishes them as typed Home Assistant entities via MQTT Discovery (device "STRI Bocas del Toro"). This is a PUBLIC repo (`AMcPherran/stri-data-bridge`).

## Layout

- `main.go` — chart definitions and the poll loop
- `internal/` — `config/` (env vars), `fetcher/` (HTTP + ETag caching), `vision/` (OCR pipeline, hardcoded crop coordinates), `homeassistant/` (discovery payloads), `mqttclient/` (publish + stale-entity prune)
- `Dockerfile` / `docker-compose.yml` — two-stage build with Tesseract; compose builds locally for dev
- `debug-images/` — OCR crop debug output when `STRI_DEBUG_IMAGES=1` (gitignored)
- `README.md` — full sensor tables and config reference

## Run

`docker compose up -d` with `MQTT_BROKER` (+ optional creds, `POLL_INTERVAL`, default 15m) in `.env`. Local `go build` needs `libtesseract-dev` + `libleptonica-dev`.

## Deploy — mid-migration, read before touching

Production runs as a Dokploy app on the dokploy LXC (10.10.1.55 on cube), NOT from this repo's compose file. There is no CI here; the image is built/pushed via Dokploy. Its image is the LAST one still on the old Harbor registry (`reg.awm.dev`) — the Harbor to GitLab (`registry.pa.awm.dev`) migration is in flight and Harbor is being decommissioned. Any deploy work should move this image to the GitLab registry, never add new `reg.awm.dev` references.

## Warnings (live system)

- The local `.env` contains real broker credentials (gitignored) — never commit or print it.
- Publishes RETAINED MQTT messages and tombstones old discovery topics on startup. Renaming sensors changes live HA entities.
- Never run a second instance with the same `MQTT_CLIENT_ID` as prod — duplicate IDs kick each other off the broker.
- MQTT discovery gotcha: set `has_entity_name: true` + short names or entity ids double up (`sensor.stri_bocas_del_toro_stri_bocas_*`).

## Conventions

No per-repo TODO file; cross-project items live in `~/agents/TO-DO.md`. Curated context: `~/agents/memory/wiki/projects/stri-data-bridge/stri-data-bridge.md` (wiki is read-only from project work). Commit style: short imperative messages; push to `origin main` (GitHub, public).
