package ramix

import (
	"context"
	"errors"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

type managedConnectionStub struct {
	id uint64

	quiesceErr   error
	quiesceCount atomic.Int32
	closeCount   atomic.Int32
	waitDone     chan struct{}
	closeOnce    sync.Once
}

func newManagedConnectionStub(id uint64) *managedConnectionStub {
	return &managedConnectionStub{id: id, waitDone: make(chan struct{})}
}

func (c *managedConnectionStub) ID() uint64              { return c.id }
func (c *managedConnectionStub) RemoteAddress() net.Addr { return nil }
func (c *managedConnectionStub) Send(context.Context, uint32, []byte) error {
	return nil
}
func (c *managedConnectionStub) quiesceReads() error {
	c.quiesceCount.Add(1)
	return c.quiesceErr
}
func (c *managedConnectionStub) stopSendsAndDrain(context.Context) error { return nil }
func (c *managedConnectionStub) requestClose(ConnectionOperation, error) {
	c.closeCount.Add(1)
	c.closeOnce.Do(func() { close(c.waitDone) })
}
func (c *managedConnectionStub) wait(ctx context.Context) error {
	select {
	case <-c.waitDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestConnectionManagerAddRemoveCountAndSnapshotAcrossShards(t *testing.T) {
	manager := newConnectionManager(4)
	connections := make([]*managedConnectionStub, 0, 12)
	for id := uint64(1); id <= 12; id++ {
		connection := newManagedConnectionStub(id)
		connections = append(connections, connection)
		manager.addConnection(connection)
	}

	if got := manager.connectionsCount(); got != 12 {
		t.Fatalf("connectionsCount() = %d, want 12", got)
	}
	if got := snapshotIDs(manager.snapshot()); !equalUint64s(got, sequence(1, 12)) {
		t.Fatalf("snapshot IDs = %v, want 1..12", got)
	}

	for _, connection := range connections[:4] {
		manager.removeConnection(connection)
	}
	if got := manager.connectionsCount(); got != 8 {
		t.Fatalf("connectionsCount() after removals = %d, want 8", got)
	}
}

func TestConnectionManagerSnapshotStaysStableWhileRegistryMutates(t *testing.T) {
	manager := newConnectionManager(3)
	first := newManagedConnectionStub(1)
	second := newManagedConnectionStub(2)
	manager.addConnection(first)
	manager.addConnection(second)

	snapshot := manager.snapshot()
	manager.removeConnection(first)
	manager.addConnection(newManagedConnectionStub(3))

	if got := snapshotIDs(snapshot); !equalUint64s(got, []uint64{1, 2}) {
		t.Fatalf("stable snapshot IDs = %v, want [1 2]", got)
	}
	if got := snapshotIDs(manager.snapshot()); !equalUint64s(got, []uint64{2, 3}) {
		t.Fatalf("current snapshot IDs = %v, want [2 3]", got)
	}
}

func TestConnectionManagerLifecycleOperationsUseManagedConnections(t *testing.T) {
	manager := newConnectionManager(2)
	first := newManagedConnectionStub(1)
	second := newManagedConnectionStub(2)
	second.quiesceErr = errors.New("deadline failed")
	manager.addConnection(first)
	manager.addConnection(second)

	errs := manager.quiesceAll()
	if len(errs) != 1 || !errors.Is(errs[0], second.quiesceErr) {
		t.Fatalf("quiesceAll() errors = %v, want second error", errs)
	}
	if first.quiesceCount.Load() != 1 || second.quiesceCount.Load() != 1 {
		t.Fatalf("quiesce counts = (%d, %d), want (1, 1)", first.quiesceCount.Load(), second.quiesceCount.Load())
	}

	manager.forceCloseAll()
	if first.closeCount.Load() != 1 || second.closeCount.Load() != 1 {
		t.Fatalf("force close counts = (%d, %d), want (1, 1)", first.closeCount.Load(), second.closeCount.Load())
	}
	if err := manager.waitAll(context.Background()); err != nil {
		t.Fatalf("waitAll() error = %v", err)
	}
}

func TestConnectionManagerWaitAllHonorsContext(t *testing.T) {
	manager := newConnectionManager(1)
	manager.addConnection(newManagedConnectionStub(1))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.waitAll(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitAll() error = %v, want %v", err, context.Canceled)
	}
}

func TestConnectionManagerWaitAllIncludesFinalizingConnections(t *testing.T) {
	manager := newConnectionManager(1)
	connection := newManagedConnectionStub(1)
	manager.addConnection(connection)
	manager.removeConnection(connection)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.waitAll(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitAll() for finalizing connection error = %v, want %v", err, context.Canceled)
	}

	connection.requestClose(OperationRead, net.ErrClosed)
	if err := manager.waitAll(context.Background()); err != nil {
		t.Fatalf("waitAll() after finalization error = %v", err)
	}
	manager.markFinalized(connection)
	if got := len(manager.finalizationSnapshot()); got != 0 {
		t.Fatalf("finalizing connection count = %d, want 0", got)
	}
}

func snapshotIDs(snapshot []managedConnection) []uint64 {
	ids := make([]uint64, len(snapshot))
	for i, connection := range snapshot {
		ids[i] = connection.ID()
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func sequence(first, last uint64) []uint64 {
	values := make([]uint64, 0, last-first+1)
	for value := first; value <= last; value++ {
		values = append(values, value)
	}
	return values
}

func equalUint64s(first, second []uint64) bool {
	if len(first) != len(second) {
		return false
	}
	for i := range first {
		if first[i] != second[i] {
			return false
		}
	}
	return true
}
