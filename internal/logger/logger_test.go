package logger

import (
	"context"
	"log/slog"
	"testing"
)

func TestLevels(t *testing.T) {
	tests := []struct {
		level string
		wantD bool
	}{
		{"debug", true},
		{"info", false},
		{"warn", false},
		{"error", false},
		{"", false}, // default info
	}
	for _, tt := range tests {
		log := New(tt.level)
		if log == nil {
			t.Fatalf("logger.New(%q) returned nil", tt.level)
		}
		enabled := log.Enabled(context.Background(), slog.LevelDebug)
		if enabled != tt.wantD {
			t.Fatalf("level %q debug enabled=%v want %v", tt.level, enabled, tt.wantD)
		}
	}
}
