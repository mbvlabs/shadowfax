package watcher

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestIsTailwindRebuildDoneLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{
			name: "done line",
			line: "Done in 40ms",
			want: true,
		},
		{
			name: "done line with microseconds",
			line: "Done in 44µs",
			want: true,
		},
		{
			name: "non-done line",
			line: "Rebuilding...",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTailwindRebuildDoneLine(tt.line)
			if got != tt.want {
				t.Fatalf("isTailwindRebuildDoneLine(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestShouldEmitTailwindRebuildDebounces(t *testing.T) {
	var last atomic.Int64

	if !shouldEmitTailwindRebuild(&last, 100*time.Millisecond) {
		t.Fatal("first signal should emit")
	}
	if shouldEmitTailwindRebuild(&last, 100*time.Millisecond) {
		t.Fatal("second immediate signal should be debounced")
	}

	time.Sleep(120 * time.Millisecond)

	if !shouldEmitTailwindRebuild(&last, 100*time.Millisecond) {
		t.Fatal("signal after debounce window should emit")
	}
}
