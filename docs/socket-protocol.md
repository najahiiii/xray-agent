# Agent WebSocket protocol v1

The agent opens one outbound WebSocket connection to the control panel. By
default `https://panel.example.com` becomes
`wss://panel.example.com/agent/ws`; `control.socket_url` can override it.

## Handshake

The HTTP upgrade request includes:

```text
Authorization: Bearer <agent token>
X-Agent-Slug: <server slug>
X-Agent-Protocol: 1
```

The gateway must authenticate the token before accepting the upgrade. Tokens
must not be placed in query parameters.

## Envelope

Every application message is JSON:

```json
{
  "version": 1,
  "id": "32-character-hex-id",
  "type": "metrics",
  "sequence": 42,
  "sent_at": "2026-07-29T10:00:00Z",
  "payload": {}
}
```

Durable agent messages have a stable `id` and monotonically increasing
`sequence`. The gateway must process each `id` idempotently and reply only
after its database transaction commits:

```json
{
  "version": 1,
  "type": "ack",
  "sent_at": "2026-07-29T10:00:01Z",
  "payload": { "message_id": "32-character-hex-id" }
}
```

Until acknowledged, messages remain in the BoltDB outbox and are replayed
after reconnect.

Snapshot messages can briefly overlap across reconnects. The gateway applies
`online` snapshots monotonically by `payload.server_time`; an older snapshot
is acknowledged but cannot replace newer online state.

## Agent to gateway

| Type            | Durable          | Payload                                                  |
| --------------- | ---------------- | -------------------------------------------------------- |
| `hello`         | No               | server slug, agent/core versions, applied config version |
| `heartbeat`     | No               | health and agent/core versions                           |
| `stats`         | Yes              | additive per-user byte deltas                            |
| `online`        | Yes, latest only | full online-user snapshot                                |
| `metrics`       | Yes              | host and Xray runtime metrics                            |
| `state_applied` | Yes, latest only | applied config version                                   |
| `command_ack`   | Yes              | command ID and success/failure result                    |
| `pong`          | No               | application-level ping reply                             |

Stats are derived from cumulative Xray counters. The baseline and outbound
event are committed in the same local BoltDB transaction. Counter rollback is
treated as an Xray restart, with the new counter value becoming the delta.

## Gateway to agent

| Type            | Payload                                                             |
| --------------- | ------------------------------------------------------------------- |
| `hello_ack`     | optional connection/session settings                                |
| `desired_state` | the existing state object (`config_version`, clients, routes, meta) |
| `command`       | the existing agent command object                                   |
| `ack`           | durable message ID committed by the gateway                         |
| `ping`          | optional application-level liveness probe                           |
| `error`         | structured gateway error                                            |

The gateway should send `desired_state` after `hello` when its version differs
from the agent's applied version. It must keep resending until it receives
`state_applied` for that version.

Commands remain persisted in the panel's `AgentCommand` table. The agent keeps
completed command acknowledgments locally, so a command replay after reconnect
returns the stored result instead of executing a destructive operation twice.

## Connection behavior

- WebSocket control ping: every 25 seconds.
- Pong/read timeout: 75 seconds.
- Reconnect: exponential backoff from 1 to 30 seconds with jitter.
- Maximum inbound message: 4 MiB.
- Only one writer goroutine writes data frames.
- The gateway should enforce one active connection per server slug.

The v1 agent runtime uses WebSocket exclusively for state, commands, heartbeat,
stats, online snapshots, and metrics.
