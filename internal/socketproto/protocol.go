package socketproto

import (
	"encoding/json"
	"time"

	"github.com/najahiiii/xray-agent/internal/model"
)

const Version = 1

const (
	TypeHello        = "hello"
	TypeHelloAck     = "hello_ack"
	TypeAck          = "ack"
	TypePing         = "ping"
	TypePong         = "pong"
	TypeDesiredState = "desired_state"
	TypeStateApplied = "state_applied"
	TypeCommand      = "command"
	TypeCommandAck   = "command_ack"
	TypeHeartbeat    = "heartbeat"
	TypeStats        = "stats"
	TypeOnline       = "online"
	TypeMetrics      = "metrics"
	TypeError        = "error"
)

// Envelope is the versioned wire format shared by the agent and socket gateway.
// ID identifies one delivery attempt across reconnects; Sequence is monotonic per
// agent outbox and lets the gateway reject stale snapshots.
type Envelope struct {
	Version  int             `json:"version"`
	ID       string          `json:"id,omitempty"`
	Type     string          `json:"type"`
	Sequence uint64          `json:"sequence,omitempty"`
	SentAt   time.Time       `json:"sent_at"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

type Hello struct {
	ServerSlug      string `json:"server_slug"`
	AgentVersion    string `json:"agent_version,omitempty"`
	XrayCoreVersion string `json:"xray_core_version,omitempty"`
	ConfigVersion   int64  `json:"config_version"`
}

type Ack struct {
	MessageID string `json:"message_id"`
}

type StateApplied struct {
	ConfigVersion int64 `json:"config_version"`
}

type CommandAck struct {
	CommandID string                `json:"command_id"`
	Ack       model.AgentCommandAck `json:"ack"`
}

func NewEnvelope(messageType string, payload any) (Envelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		Version: Version,
		Type:    messageType,
		SentAt:  time.Now().UTC(),
		Payload: raw,
	}, nil
}
