//go:build !production

package session

import (
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/stretchr/testify/mock"
)

func (gs *GameSession) IsRetiredForTest() bool {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	return gs.retired
}

func (gs *GameSession) EndWithSettlementForTest(settlement *protocol.GameSettlementDTO) {
	gs.mu.Lock()
	gs.state = GameStateEnded
	gs.settlement = cloneGameSettlement(settlement)
	gs.room.ResetAfterGame()
	gs.mu.Unlock()
	gs.stopTimer()
}

// MockGameSessionStore 游戏会话存储 mock
type MockGameSessionStore struct {
	mock.Mock
}

func (m *MockGameSessionStore) SaveGameSession(roomCode string, data any) error {
	args := m.Called(roomCode, data)
	return args.Error(0)
}

func (m *MockGameSessionStore) LoadGameSession(roomCode string) (any, error) {
	args := m.Called(roomCode)
	return args.Get(0), args.Error(1)
}

func (m *MockGameSessionStore) DeleteGameSession(roomCode string) error {
	args := m.Called(roomCode)
	return args.Error(0)
}
