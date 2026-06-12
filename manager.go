package ramix

import (
	"context"
	"sync"
)

type connectionManager struct {
	connectionGroups []*connectionGroup
	finalizingMu     sync.RWMutex
	finalizing       map[uint64]managedConnection
	finalizingChange chan struct{}
}

type connectionGroup struct {
	connections map[uint64]managedConnection
	lock        sync.RWMutex
}

func newConnectionManager(connectionGroupsCount int) *connectionManager {
	connectionGroups := make([]*connectionGroup, connectionGroupsCount)
	for i := range connectionGroups {
		connectionGroups[i] = &connectionGroup{
			connections: make(map[uint64]managedConnection),
		}
	}
	return &connectionManager{
		connectionGroups: connectionGroups,
		finalizing:       make(map[uint64]managedConnection),
		finalizingChange: make(chan struct{}),
	}
}

func (m *connectionManager) selectGroup(connection managedConnection) *connectionGroup {
	return m.connectionGroups[connection.ID()%uint64(len(m.connectionGroups))]
}

func (m *connectionManager) addConnection(connection managedConnection) {
	group := m.selectGroup(connection)
	group.lock.Lock()
	group.connections[connection.ID()] = connection
	group.lock.Unlock()
}

func (m *connectionManager) removeConnection(connection managedConnection) {
	m.finalizingMu.Lock()
	m.finalizing[connection.ID()] = connection
	m.finalizingMu.Unlock()

	group := m.selectGroup(connection)
	group.lock.Lock()
	delete(group.connections, connection.ID())
	group.lock.Unlock()
}

func (m *connectionManager) markFinalized(connection managedConnection) {
	m.finalizingMu.Lock()
	delete(m.finalizing, connection.ID())
	close(m.finalizingChange)
	m.finalizingChange = make(chan struct{})
	m.finalizingMu.Unlock()
}

func (m *connectionManager) connectionsCount() int {
	total := 0
	for _, group := range m.connectionGroups {
		group.lock.RLock()
		total += len(group.connections)
		group.lock.RUnlock()
	}
	return total
}

func (m *connectionManager) snapshot() []managedConnection {
	connections := make([]managedConnection, 0, m.connectionsCount())
	for _, group := range m.connectionGroups {
		group.lock.RLock()
		for _, connection := range group.connections {
			connections = append(connections, connection)
		}
		group.lock.RUnlock()
	}
	return connections
}

func (m *connectionManager) quiesceAll() []error {
	var errs []error
	for _, connection := range m.snapshot() {
		if err := connection.quiesceReads(); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func (m *connectionManager) forceCloseAll() {
	for _, connection := range m.snapshot() {
		connection.requestClose(OperationRead, ErrServerStopping)
	}
}

func (m *connectionManager) waitAll(ctx context.Context) error {
	for {
		connections := m.snapshot()
		finalizing, finalizingChange := m.finalizationState()
		connections = append(connections, finalizing...)
		if len(connections) == 0 {
			return nil
		}
		for _, connection := range connections {
			if err := connection.wait(ctx); err != nil {
				return err
			}
		}

		finalizing, finalizingChange = m.finalizationState()
		if len(finalizing) == 0 {
			return nil
		}
		select {
		case <-finalizingChange:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (m *connectionManager) finalizationSnapshot() []managedConnection {
	connections, _ := m.finalizationState()
	return connections
}

func (m *connectionManager) finalizationState() ([]managedConnection, <-chan struct{}) {
	m.finalizingMu.RLock()
	defer m.finalizingMu.RUnlock()

	connections := make([]managedConnection, 0, len(m.finalizing))
	for _, connection := range m.finalizing {
		connections = append(connections, connection)
	}
	return connections, m.finalizingChange
}
