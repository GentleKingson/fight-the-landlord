package handler

import (
	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

const (
	operationalNormal      = "normal"
	operationalDraining    = "draining"
	operationalMaintenance = "maintenance"
)

type operationalStateProvider interface {
	OperationalState() string
}

type operationalAdmissionProvider interface {
	AcquireOperationalAdmission(allowDraining bool) (func(), string, bool)
}

type playerModerationProvider interface {
	IsPlayerMuted(playerID string) bool
	IsPlayerBanned(playerID string) bool
}

func currentOperationalState(server types.ServerInterface) string {
	if server == nil {
		return operationalNormal
	}
	if state, ok := providedOperationalState(server); ok {
		return state
	}
	if server.IsMaintenanceMode() {
		return operationalMaintenance
	}
	return operationalNormal
}

func providedOperationalState(server types.ServerInterface) (string, bool) {
	provider, ok := server.(operationalStateProvider)
	if !ok {
		return operationalNormal, false
	}
	switch provider.OperationalState() {
	case operationalDraining:
		return operationalDraining, true
	case operationalMaintenance:
		return operationalMaintenance, true
	default:
		return operationalNormal, true
	}
}

func operationalAdmissionErrorCode(state string) int {
	if state == operationalDraining {
		return protocol.ErrCodeServerDraining
	}
	return protocol.ErrCodeServerMaintenance
}

func acquireOperationalAdmission(server types.ServerInterface, allowDraining bool) (func(), string, bool) {
	provider, ok := server.(operationalAdmissionProvider)
	if ok {
		return provider.AcquireOperationalAdmission(allowDraining)
	}
	state := currentOperationalState(server)
	if state != operationalNormal && !(allowDraining && state == operationalDraining) {
		return nil, state, false
	}
	return func() {}, state, true
}

func acquireReadyAdmission(server types.ServerInterface) (func(), string, bool) {
	if provider, ok := server.(operationalAdmissionProvider); ok {
		return provider.AcquireOperationalAdmission(false)
	}
	state, controlled := providedOperationalState(server)
	if controlled && state != operationalNormal {
		return nil, state, false
	}
	return func() {}, operationalNormal, true
}

func playerMuted(server types.ServerInterface, playerID string) bool {
	provider, ok := server.(playerModerationProvider)
	return ok && provider.IsPlayerMuted(playerID)
}

func playerBanned(server types.ServerInterface, playerID string) bool {
	provider, ok := server.(playerModerationProvider)
	return ok && provider.IsPlayerBanned(playerID)
}
