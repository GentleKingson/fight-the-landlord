//go:build !production

package room

import (
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/palemoky/fight-the-landlord/internal/types"
)

// MockRoomManager 房间管理器 mock
type MockRoomManager struct {
	mock.Mock
}

func (m *MockRoomManager) LeaveRoom(client types.ClientInterface) {
	m.Called(client)
}

func (m *MockRoomManager) CreateRoom(client types.ClientInterface) (any, error) {
	args := m.Called(client)
	return args.Get(0), args.Error(1)
}

func (m *MockRoomManager) JoinRoom(client types.ClientInterface, code string) (any, error) {
	args := m.Called(client, code)
	return args.Get(0), args.Error(1)
}

func (m *MockRoomManager) SetPlayerReady(client types.ClientInterface, ready bool) error {
	args := m.Called(client, ready)
	return args.Error(0)
}

func (m *MockRoomManager) GetRoom(code string) any {
	args := m.Called(code)
	return args.Get(0)
}

func (m *MockRoomManager) GetRoomList() []any {
	args := m.Called()
	return args.Get(0).([]any)
}

func (m *MockRoomManager) GetRoomByPlayerID(playerID string) any {
	args := m.Called(playerID)
	return args.Get(0)
}

func (m *MockRoomManager) GetActiveGamesCount() int {
	args := m.Called()
	return args.Int(0)
}

// MockMatcher 匹配器 mock
type MockMatcher struct {
	mock.Mock
}

func (m *MockMatcher) AddToQueue(client types.ClientInterface) {
	m.Called(client)
}

// NewMockRoom 创建测试用的 Room
func NewMockRoom(code string, client types.ClientInterface) *Room {
	room := newRoom(code, time.Now())
	if client != nil {
		player := newRoomPlayer(client, 0)
		room.players[player.ID] = player
		room.playerOrder = append(room.playerOrder, player.ID)
	}
	return room
}

func (r *Room) AddPlayerForTest(client types.ClientInterface, seat int, ready bool) {
	player := newRoomPlayer(client, seat)
	player.Ready = ready
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.players[player.ID]; !exists {
		r.playerOrder = append(r.playerOrder, player.ID)
	}
	r.players[player.ID] = player
}

func (r *Room) SetPlayerOrderForTest(playerOrder []string) {
	r.mu.Lock()
	r.playerOrder = append([]string(nil), playerOrder...)
	r.mu.Unlock()
}

func (r *Room) SetPlayerReadyForTest(playerID string, ready bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	player, ok := r.players[playerID]
	if !ok || player == nil {
		return false
	}
	player.Ready = ready
	return true
}

func (r *Room) PlayerForTest(playerID string) (PlayerSnapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	player, ok := r.players[playerID]
	if !ok || player == nil {
		return PlayerSnapshot{}, false
	}
	return snapshotPlayer(player), true
}

func (r *Room) SetCreatedAtForTest(createdAt time.Time) {
	r.mu.Lock()
	r.CreatedAt = createdAt
	r.mu.Unlock()
}

// AddRoomForTest 添加房间用于测试
func (rm *RoomManager) AddRoomForTest(room *Room) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.rooms[room.Code] = room
}
