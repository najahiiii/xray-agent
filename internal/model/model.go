package model

import "time"

type State struct {
	ConfigVersion int64          `json:"config_version"`
	Clients       []Client       `json:"clients"`
	Meta          map[string]any `json:"meta,omitempty"`
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

type UserUsage struct {
	Email    string `json:"email"`
	Uplink   int64  `json:"uplink"`
	Downlink int64  `json:"downlink"`
}
