package model

import (
	"encoding/json"
	"testing"
)

func TestStateJSONRoundTrip(t *testing.T) {
	orig := State{
		ConfigVersion: 1,
		Clients: []Client{
			{Proto: "vless", ID: "uuid", Email: "user@example.com"},
		},
		Meta: map[string]any{"foo": "bar"},
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded State
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ConfigVersion != orig.ConfigVersion || len(decoded.Clients) != 1 || decoded.Clients[0].Email != orig.Clients[0].Email {
		t.Fatalf("round trip mismatch: %+v", decoded)
	}
}
