//go:build !production

package testutil

import (
	"github.com/stretchr/testify/mock"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

// MockServer 实现 types.ServerContext 的 mock
type MockServer struct {
	mock.Mock
}

func (m *MockServer) IsMaintenanceMode() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockServer) GetOnlineCount() int {
	args := m.Called()
	return args.Int(0)
}

func (m *MockServer) BroadcastToLobby(msg *protocol.Message) {
	m.Called(msg)
}

func (m *MockServer) GetClientByID(id string) types.ClientInterface {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(types.ClientInterface)
}

func (m *MockServer) RegisterClient(id string, client types.ClientInterface) {
	m.Called(id, client)
}

func (m *MockServer) UnregisterClient(id string, client types.ClientInterface) bool {
	args := m.Called(id, client)
	return args.Bool(0)
}

func (m *MockServer) RebindClient(temporaryID, playerID, playerName, roomCode string, client types.ClientInterface) (types.ClientInterface, error) {
	args := m.Called(temporaryID, playerID, playerName, roomCode, client)
	var previous types.ClientInterface
	if args.Get(0) != nil {
		previous = args.Get(0).(types.ClientInterface)
	}
	return previous, args.Error(1)
}

func (m *MockServer) RollbackRebindClient(temporaryID, temporaryName, playerID, roomCode string, client, previous types.ClientInterface) error {
	args := m.Called(temporaryID, temporaryName, playerID, roomCode, client, previous)
	return args.Error(0)
}
