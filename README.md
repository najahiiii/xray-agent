# xray-agent (Go)

A robust provisioning agent for Xray nodes. It pulls state from a control-plane, manages users through Xray’s HandlerService, collects per-user usage via Xray API/Stats, sends heartbeat & stats back.

## Features

- Multi-protocol aware: VLESS, VMess, Trojan (via separate inbound tags)
- HandlerService-based client apply (no config file mutations or reloads)
- Periodic state reconcile, stats push, and heartbeat
- Per-user usage based on `email` (works across protocols)
- Simple, dependency-light build

## Config

See [packaging/config.example.yaml](packaging/config.example.yaml).

### Client apply path

The agent always reconciles clients via Xray’s gRPC HandlerService, adding/removing users on the fly without touching the JSON config file or reloading the daemon. Ensure HandlerService is enabled and reachable at `xray.api_server`. Deployments assume Xray listens on localhost so this plaintext gRPC link never leaves the host.

## Systemd

See [packaging/xray-agent.service](packaging/xray-agent.service).

## Build

```bash
go build -o xray-agent ./
```

## Run

```bash
./xray-agent -config /etc/xray-agent/config.yaml
```

## Expected Control-Plane API

- `GET  /api/agents/{server_slug}/state`

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

- `POST /api/agents/{server_slug}/stats`

  ```json
  {
    "server_time": "2025-11-07T15:01:00Z",
    "users": [{ "email": "user_1@planA", "uplink": 123, "downlink": 456 }]
  }
  ```

- `POST /api/agents/{server_slug}/heartbeat`

  ```json
  { "ok": true }
  ```
