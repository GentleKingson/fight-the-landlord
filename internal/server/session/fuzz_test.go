package session

import "testing"

func FuzzReconnectCredentialInput(f *testing.F) {
	f.Add("", "")
	f.Add("not-a-token", "player-1")
	f.Add("valid", "player-1")
	f.Add("0000000000000000000000000000000000000000000000000000000000000000", "player-1")

	f.Fuzz(func(t *testing.T, token, playerID string) {
		if len(token) > 4096 || len(playerID) > 4096 {
			t.Skip()
		}
		manager := NewSessionManager()
		t.Cleanup(func() { _ = manager.Close() })
		original := manager.MustCreateSession("player-1", "Player")
		originalToken := original.ReconnectToken
		manager.SetOffline(original.PlayerID)
		if token == "valid" {
			token = originalToken
		}

		_ = manager.GetSessionByToken(token)
		_ = manager.CanReconnect(token, playerID)
		_, err := manager.RestoreSession(token, playerID, "temporary")
		if err == nil && (token != originalToken || playerID != original.PlayerID) {
			t.Fatal("unexpected reconnect credential was accepted")
		}
	})
}
