package model

import (
	"reflect"
	"testing"
)

func TestNormalizeRouteRulesKeepsLastOccurrence(t *testing.T) {
	t.Parallel()

	routes := []RouteRule{
		{Tag: "re-route-ipv4", OutboundTag: "direct"},
		{Tag: "re-route-domain", OutboundTag: "blocked"},
		{Tag: "re-route-ipv4", OutboundTag: "proxy"},
	}

	normalized, duplicateTags := NormalizeRouteRules(routes)

	wantRoutes := []RouteRule{
		{Tag: "re-route-domain", OutboundTag: "blocked"},
		{Tag: "re-route-ipv4", OutboundTag: "proxy"},
	}
	if !reflect.DeepEqual(normalized, wantRoutes) {
		t.Fatalf("normalized routes mismatch: got %#v want %#v", normalized, wantRoutes)
	}

	wantDuplicateTags := []string{"re-route-ipv4"}
	if !reflect.DeepEqual(duplicateTags, wantDuplicateTags) {
		t.Fatalf("duplicate tags mismatch: got %#v want %#v", duplicateTags, wantDuplicateTags)
	}
}
