package ingest

import (
	"testing"
	"time"
)

func TestIsStale(t *testing.T) {
	base := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name         string
		incomingTime time.Time
		incomingSeq  uint64
		currentTime  time.Time
		currentSeq   uint64
		want         bool
	}{
		{"older timestamp", base.Add(-time.Second), 100, base, 1, true},
		{"newer timestamp", base.Add(time.Second), 0, base, 100, false},
		{"same timestamp older sequence", base, 1, base, 2, true},
		{"same timestamp same sequence", base, 2, base, 2, false},
		{"same timestamp newer sequence", base, 3, base, 2, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isStale(tt.incomingTime, tt.incomingSeq, tt.currentTime, tt.currentSeq); got != tt.want {
				t.Fatalf("isStale() = %v, want %v", got, tt.want)
			}
		})
	}
}
