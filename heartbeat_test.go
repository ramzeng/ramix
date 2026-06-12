package ramix

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHeartbeatAliveBoundary(t *testing.T) {
	now := time.Unix(100, 0)
	activity := newActivityClock(func() time.Time { return now })
	activity.refresh()

	now = now.Add(9 * time.Second)
	if !activity.alive(10 * time.Second) {
		t.Fatal("connection should be alive")
	}

	now = now.Add(2 * time.Second)
	if activity.alive(10 * time.Second) {
		t.Fatal("connection should be expired")
	}
}

func TestHeartbeatRefreshIsConnectionOwned(t *testing.T) {
	firstNow := time.Unix(100, 0)
	secondNow := time.Unix(200, 0)
	first := newActivityClock(func() time.Time { return firstNow })
	second := newActivityClock(func() time.Time { return secondNow })
	first.refresh()
	second.refresh()

	firstNow = firstNow.Add(20 * time.Second)
	secondNow = secondNow.Add(5 * time.Second)
	if first.alive(10 * time.Second) {
		t.Fatal("first connection should be expired")
	}
	if !second.alive(10 * time.Second) {
		t.Fatal("second connection should still be alive")
	}
}

func TestHeartbeatExpirationRequestsCloseOnce(t *testing.T) {
	now := time.Unix(100, 0)
	transport := newFakeLifecycleTransport()
	_, connection := newLifecycleTestConnection(t, transport, 1)
	connection.activity = newActivityClock(func() time.Time { return now })
	connection.refreshActivity()

	now = now.Add(3 * time.Hour)
	connection.checkHeartbeat()
	connection.checkHeartbeat()

	if got := transport.closeCount.Load(); got != 1 {
		t.Fatalf("transport close count = %d, want 1", got)
	}
	op, err := connection.closeReason()
	if op != OperationHeartbeat || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close reason = (%q, %v), want heartbeat deadline", op, err)
	}
}

func TestHeartbeatStopsWhenReadsQuiesce(t *testing.T) {
	transport := newFakeLifecycleTransport()
	server, connection := newLifecycleTestConnection(t, transport, 1)
	startLifecycleTestConnection(server, connection, transport)

	if err := connection.quiesceReads(); err != nil {
		t.Fatalf("quiesceReads() error = %v", err)
	}
	if err := connection.stopSendsAndDrain(context.Background()); err != nil {
		t.Fatalf("stopSendsAndDrain() error = %v", err)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := connection.wait(waitCtx); err != nil {
		t.Fatalf("wait() error = %v", err)
	}
	if got := transport.readDeadlineCount.Load(); got != 1 {
		t.Fatalf("read deadline count = %d, want 1", got)
	}
}

func TestHeartbeatExpirationDoesNotCloseQuiescingConnection(t *testing.T) {
	now := time.Unix(100, 0)
	transport := newFakeLifecycleTransport()
	_, connection := newLifecycleTestConnection(t, transport, 1)
	connection.activity = newActivityClock(func() time.Time { return now })
	connection.refreshActivity()

	now = now.Add(3 * time.Hour)
	if err := connection.quiesceReads(); err != nil {
		t.Fatalf("quiesceReads() error = %v", err)
	}
	connection.checkHeartbeat()

	if got := transport.closeCount.Load(); got != 0 {
		t.Fatalf("transport close count = %d, want 0", got)
	}
	if got := connection.connectionState(); got != connectionDraining {
		t.Fatalf("connection state = %v, want draining", got)
	}
}
