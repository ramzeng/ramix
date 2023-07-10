package ramix

import (
	"sync"
)

type connectionManager struct {
	connections map[uint64]*Connection
	lock        sync.RWMutex
}

func (cm *connectionManager) AddConnection(connection *Connection) {
	cm.lock.Lock()
	defer cm.lock.Unlock()

	cm.connections[connection.ID] = connection
}

func (cm *connectionManager) RemoveConnection(connection *Connection) {
	cm.lock.Lock()
	defer cm.lock.Unlock()

	delete(cm.connections, connection.ID)
}

func (cm *connectionManager) ClearConnections() {
	cm.lock.Lock()
	defer cm.lock.Unlock()

	cm.connections = make(map[uint64]*Connection)
}

func (cm *connectionManager) Connection(id uint64) *Connection {
	cm.lock.RLock()
	defer cm.lock.RUnlock()

	return cm.connections[id]
}

func (cm *connectionManager) ConnectionsCount() int {
	cm.lock.RLock()
	defer cm.lock.RUnlock()
	return len(cm.connections)
}
