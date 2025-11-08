# xray-agent

Provisioning/telemetry side for Xray nodes. The agent stays on the same host as Xray, pulls desired state from a control-plane, reconciles users through Xray’s HandlerService gRPC API, streams usage via StatsService, and periodically reports stats + heartbeats upstream.

## Highlights

- **Single source of truth** – Control-plane drives `state` JSON; the agent diffs and applies only the deltas.
- **HandlerService apply** – Users are added/removed live via gRPC; no config.json juggling or daemon reloads.
- **Stats over gRPC** – User uplink/downlink counters come from the native StatsService (fast + no subprocesses).
- **Protocol aware** – VLESS / VMess / Trojan clients mapped to dedicated inbound tags for per-protocol isolation.
- **Lightweight** – Pure Go binary; depends only on Xray’s gRPC endpoints exposed on `localhost`.

## Architecture

```plaintext
Control Plane ──HTTP(S)──► Agent ──gRPC──► Xray HandlerService / StatsService
                                  │
                                  └─ systemd supervises agent lifecycle
```

1. `state_loop`: poll `/api/agents/{slug}/state`, compare with cached `config_version`, push diffs through HandlerService.
2. `stats_loop`: query StatsService for per-email counters and POST them back to `/stats`.
3. `heartbeat_loop`: POST `/heartbeat` so the control-plane can detect liveness.

All gRPC traffic is assumed to stay on `localhost`; bind Xray’s API server accordingly.

## Configuration

See [packaging/config.example.yaml](packaging/config.example.yaml) for the full schema. High-level knobs:

```yaml
control:
  base_url: https://panel.example.com
  token: AGENT_TOKEN
  server_slug: sg-1
  tls_insecure: false

xray:
  binary: /usr/local/bin/xray # still used for stats reset checks if needed
  api_server: 127.0.0.1:10085 # HandlerService + StatsService listener
  api_timeout_sec: 5
  stats_reset_each_push: true # tell StatsService to reset counters after read
  inbound_tags:
    vless: vless-ws
    vmess: vmess-ws
    trojan: trojan-ws

intervals:
  state_sec: 15
  stats_sec: 60
  heartbeat_sec: 30

logging:
  level: info
```

### Client reconciliation

HandlerService must be enabled in your Xray config:

```json
{
  "api": {
    "tag": "xray-api",
    "services": ["HandlerService", "LoggerService", "StatsService"]
  },
  "stats": {}
}
```

The agent only needs HandlerService for add/remove and StatsService for counters. Keep the listener on `127.0.0.1` (or a UNIX socket) because the agent currently dials with plaintext credentials.

## Build / Run

```bash
go build -o xray-agent ./
sudo ./xray-agent -config /etc/xray-agent/config.yaml
```

Systemd unit: [packaging/xray-agent.service](packaging/xray-agent.service).

## Control-plane contract

### `GET /api/agents/{server_slug}/state`

```json
{
  "config_version": 12,
  "clients": [
    { "proto": "vless", "id": "UUID", "email": "user_1@planA" },
    { "proto": "vmess", "id": "UUID", "email": "user_2@planB" },
    { "proto": "trojan", "password": "pass123", "email": "user_3@planC" }
  ],
  "meta": { "ws_path": "/ws" }
}
```

### `POST /api/agents/{server_slug}/stats`

```json
{
  "server_time": "2025-11-07T15:01:00Z",
  "users": [{ "email": "user_1@planA", "uplink": 123, "downlink": 456 }]
}
```

### `POST /api/agents/{server_slug}/heartbeat`

```json
{ "ok": true }
```

## Development

- Go ≥ 1.25.3 (module declares 1.25.3; see `go.mod`).
- Run `go test ./...` before submitting changes.
- Formatter: `gofmt` (already wired via CI scripts).
