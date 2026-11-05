package service

import (
	"testing"
	"time"
)

func TestHeartbeatRegistryTracksStaleness(t *testing.T) {
	r := NewHeartbeatRegistry()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }

	r.Beat("scheduler", 60*time.Second)
	r.Beat("watchdog", 12*time.Minute)

	snapshot := r.Snapshot()
	if len(snapshot) != 2 || snapshot[0].Service != "scheduler" || snapshot[1].Service != "watchdog" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if !r.Healthy() {
		t.Fatalf("fresh heartbeats should be healthy: %+v", snapshot)
	}

	now = now.Add(61 * time.Second)
	snapshot = r.Snapshot()
	if snapshot[0].Healthy {
		t.Fatalf("scheduler should be stale after 61s: %+v", snapshot[0])
	}
	if !snapshot[1].Healthy {
		t.Fatalf("watchdog should still be fresh: %+v", snapshot[1])
	}
	if r.Healthy() {
		t.Fatal("registry with a stale loop must not report healthy")
	}

	r.Beat("scheduler", 60*time.Second)
	if !r.Healthy() {
		t.Fatal("a fresh beat should restore health")
	}
}

func TestNilHeartbeatRegistryIsSafe(t *testing.T) {
	var r *HeartbeatRegistry
	r.Beat("scheduler", time.Minute)
	if got := r.Snapshot(); got != nil {
		t.Fatalf("nil registry snapshot = %+v, want nil", got)
	}
	if !r.Healthy() {
		t.Fatal("nil registry should not report unhealthy")
	}
}
