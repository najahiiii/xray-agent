package model

import "time"

type State struct {
	ConfigVersion int64          `json:"config_version"`
	Clients       []Client       `json:"clients"`
	Routes        []RouteRule    `json:"routes,omitempty"`
	Meta          map[string]any `json:"meta,omitempty"`
}

type AgentCommandType string

const (
	AgentCommandTypeRestartCore  AgentCommandType = "RESTART_CORE"
	AgentCommandTypeRestartAgent AgentCommandType = "RESTART_AGENT"
)

type AgentCommand struct {
	ID          string           `json:"id"`
	Type        AgentCommandType `json:"type"`
	RequestedAt time.Time        `json:"requested_at"`
}

type AgentCommandAckStatus string

const (
	AgentCommandAckSucceeded AgentCommandAckStatus = "SUCCEEDED"
	AgentCommandAckFailed    AgentCommandAckStatus = "FAILED"
)

type AgentCommandAck struct {
	Status       AgentCommandAckStatus `json:"status"`
	ErrorMessage string                `json:"error_message,omitempty"`
	Result       map[string]any        `json:"result,omitempty"`
}

type Client struct {
	Proto    string `json:"proto"`
	ID       string `json:"id,omitempty"`
	Password string `json:"password,omitempty"`
	Email    string `json:"email"`
}

type StatsPush struct {
	ServerTime time.Time   `json:"server_time"`
	Users      []UserUsage `json:"users"`
}

type HeartbeatPush struct {
	OK           bool   `json:"ok"`
	AgentVersion string `json:"agent_version,omitempty"`
}

type ServerMetricPush struct {
	ServerTime        time.Time     `json:"server_time"`
	CPUPercent        *float64      `json:"cpu_percent,omitempty"`
	MemoryPercent     *float64      `json:"memory_percent,omitempty"`
	BandwidthDownMbps *float64      `json:"bandwidth_down_mbps,omitempty"`
	BandwidthUpMbps   *float64      `json:"bandwidth_up_mbps,omitempty"`
	XraySysStats      *XraySysStats `json:"xray_sys_stats,omitempty"`
}

type UserUsage struct {
	Email    string `json:"email"`
	Uplink   int64  `json:"uplink"`
	Downlink int64  `json:"downlink"`
}

type RouteRule struct {
	Tag         string   `json:"tag"`
	OutboundTag string   `json:"outbound_tag,omitempty"`
	BalancerTag string   `json:"balancer_tag,omitempty"`
	Domain      []string `json:"domain,omitempty"`
	IP          []string `json:"ip,omitempty"`
	Port        string   `json:"port,omitempty"`
	SourcePort  string   `json:"source_port,omitempty"`
	InboundTag  []string `json:"inbound_tag,omitempty"`
	Protocol    []string `json:"protocol,omitempty"`
}

type XraySysStats struct {
	NumGoroutine uint32 `json:"num_goroutine"`
	NumGC        uint32 `json:"num_gc"`
	Alloc        uint64 `json:"alloc"`
	TotalAlloc   uint64 `json:"total_alloc"`
	Sys          uint64 `json:"sys"`
	Mallocs      uint64 `json:"mallocs"`
	Frees        uint64 `json:"frees"`
	LiveObjects  uint64 `json:"live_objects"`
	PauseTotalNs uint64 `json:"pause_total_ns"`
	Uptime       uint32 `json:"uptime"`
}
