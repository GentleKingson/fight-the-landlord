package server

import (
	"log/slog"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
)

const (
	operationalNormal uint32 = iota
	operationalDraining
	operationalMaintenance
)

const (
	OperationalStateNormal      = "normal"
	OperationalStateDraining    = "draining"
	OperationalStateMaintenance = "maintenance"
)

// OperationalState reports the current admission state. The zero value is
// normal so a minimally constructed Server remains usable in focused tests.
func (s *Server) OperationalState() string {
	if s == nil {
		return OperationalStateNormal
	}
	switch s.operationalState.Load() {
	case operationalDraining:
		return OperationalStateDraining
	case operationalMaintenance:
		return OperationalStateMaintenance
	default:
		return OperationalStateNormal
	}
}

func (s *Server) setOperationalState(next uint32) bool {
	if s == nil || next > operationalMaintenance {
		return false
	}
	s.operationalMu.Lock()
	defer s.operationalMu.Unlock()
	previous := s.operationalState.Swap(next)
	if previous == next {
		return false
	}

	state := operationalStateName(next)
	maintenance := next == operationalMaintenance
	s.Broadcast(codec.MustNewMessage(protocol.MsgMaintenancePush, protocol.MaintenancePayload{
		Maintenance: maintenance,
	}))

	message := ""
	if next == operationalDraining {
		message = "服务器正在排空，已停止新房间、新匹配和人机练习"
	} else if next == operationalMaintenance {
		message = "服务器正在维护，已停止新游戏入口"
	}
	if message != "" {
		code := protocol.ErrCodeServerDraining
		if next == operationalMaintenance {
			code = protocol.ErrCodeServerMaintenance
		}
		s.Broadcast(codec.MustNewMessage(protocol.MsgError, protocol.ErrorPayload{
			Code:    code,
			Message: message,
		}))
	}

	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("operational state changed",
		"event", "operational_state_changed",
		"previous_state", operationalStateName(previous),
		"state", state,
	)
	return true
}

func operationalStateName(state uint32) string {
	switch state {
	case operationalDraining:
		return OperationalStateDraining
	case operationalMaintenance:
		return OperationalStateMaintenance
	default:
		return OperationalStateNormal
	}
}

// EnterDrainingMode stops new game admission while preserving current games
// and reconnects.
func (s *Server) EnterDrainingMode() bool {
	return s.setOperationalState(operationalDraining)
}

// EnterMaintenanceMode keeps the legacy API while moving to the three-state
// operational control.
func (s *Server) EnterMaintenanceMode() {
	s.setOperationalState(operationalMaintenance)
}

// ResumeNormalOperation re-enables new game admission.
func (s *Server) ResumeNormalOperation() bool {
	return s.setOperationalState(operationalNormal)
}

// IsMaintenanceMode is retained for existing clients that only understand the
// legacy boolean maintenance status.
func (s *Server) IsMaintenanceMode() bool {
	return s.OperationalState() == OperationalStateMaintenance
}
