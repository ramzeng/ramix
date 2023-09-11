package ramix

import (
	"sync"
)

func newConnectionManager(connectionGroupsCount int) *connectionManager {
	connectionGroups := make([]*connectionGroup, connectionGroupsCount)

	for step := 0; step < connectionGroupsCount; step++ {
		connectionGroups[step] = &connectionGroup{
			connections: make(map[uint64]*Connection),
		}
	}

	return &connectionManager{
		connectionGroups: connectionGroups,
	}
}

type connectionManager struct {
	connectionGroups []*connectionGroup
}

func (cm *connectionManager) selectGroup(connection *Connection) *connectionGroup {
	return cm.connectionGroups[connection.ID%uint64(len(cm.connectionGroups))]
}

func (cm *connectionManager) addConnection(connection *Connection) {
	cm.selectGroup(connection).addConnection(connection)
}

func (cm *connectionManager) removeConnection(connection *Connection) {
	cm.selectGroup(connection).removeConnection(connection)
}

func (cm *connectionManager) clearConnections() {
	for _, group := range cm.connectionGroups {
		group.clearConnections()
	}
}

func (cm *connectionManager) connectionsCount() int {
	connectionsCount := 0

	for _, group := range cm.connectionGroups {
		connectionsCount += group.connectionsCount()
	}

	return connectionsCount
}

type connectionGroup struct {
	connections map[uint64]*Connection
	lock        sync.RWMutex
}

func (cm *connectionGroup) addConnection(connection *Connection) {
	cm.lock.Lock()
	defer cm.lock.Unlock()

	cm.connections[connection.ID] = connection
}

func (cm *connectionGroup) removeConnection(connection *Connection) {
	cm.lock.Lock()
	defer cm.lock.Unlock()

	delete(cm.connections, connection.ID)
}

func (cm *connectionGroup) clearConnections() {
	cm.lock.Lock()
	defer cm.lock.Unlock()

	cm.connections = make(map[uint64]*Connection)
}

func (cm *connectionGroup) connectionsCount() int {
	cm.lock.RLock()
	defer cm.lock.RUnlock()

	return len(cm.connections)
}
