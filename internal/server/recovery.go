package server

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/bot"
	"github.com/palemoky/fight-the-landlord/internal/server/session"
	"github.com/palemoky/fight-the-landlord/internal/server/storage"
	"github.com/palemoky/fight-the-landlord/internal/types"
)

func (s *Server) restoreGames(ctx context.Context, engine bot.DecisionEngine) error {
	if s.recoveryLoaded {
		return nil
	}
	records, err := s.redisStore.LoadGames(ctx)
	if err != nil {
		return err
	}
	// Restore older settlements first; a later active game owns the association.
	sort.Slice(records, func(i, j int) bool { return records[i].SavedAt.Before(records[j].SavedAt) })
	active := make(map[string]bool)
	for _, record := range records {
		snapshot, err := session.DecodeSnapshot(record)
		if err != nil {
			return err
		}
		expires := record.ExpiresAt
		if expires.IsZero() {
			expires = record.SavedAt.Add(snapshot.Config.OfflineRoomRetentionDuration())
		}
		if !expires.After(time.Now()) {
			if err := s.redisStore.DeleteExpiredGame(ctx, record); err != nil {
				return err
			}
			continue
		}
		if err := s.restoreRecord(record, snapshot, engine, active); err != nil {
			return err
		}
	}
	for _, gs := range s.handler.GameSessions() {
		gs.Resume()
	}
	s.recoveryLoaded = true
	return nil
}

func (s *Server) restoreRecord(record *storage.GameRecord, snapshot session.Snapshot, engine bot.DecisionEngine, active map[string]bool) error {
	gs, r, err := session.RestoreGame(record, s.leaderboard, func(p session.SnapshotPlayer) types.ClientInterface {
		if p.IsBot {
			return bot.RestoreClient(p.ID, p.Name, engine)
		}
		c := NewClient(s, nil)
		c.ID, c.Name, c.RoomID = p.ID, p.Name, record.RoomCode
		c.Close() // An offline placeholder cannot deliver messages or own a socket.
		return c
	})
	if err != nil {
		return err
	}
	if err := s.roomManager.RestoreRoom(r); err != nil {
		return err
	}
	for _, p := range snapshot.Players {
		if p.IsBot {
			b := r.Players[p.ID].Client.(*bot.BotClient)
			b.SetRoom(r.Code)
			b.SetSession(gs)
			gs.RestoreBot(b)
			continue
		}
		if !record.Ended && active[p.ID] {
			return fmt.Errorf("player %s belongs to multiple recovered games", p.ID)
		}
		s.sessionManager.EnsureSession(p.ID, p.Name)
		if !active[p.ID] {
			s.sessionManager.SetRoom(p.ID, r.Code)
			if record.Ended {
				s.sessionManager.SetRoom(p.ID, "")
			}
		}
		if !record.Ended {
			active[p.ID] = true
		}
		s.sessionManager.SetOffline(p.ID)
	}
	s.handler.SetGameSession(r.Code, gs)
	return nil
}

func (s *Server) settlementLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.redisStore.DrainSettlements(attempt, s.leaderboard)
		if err == nil {
			err = s.cleanPersistedGames(attempt)
		}
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("持久战绩队列等待 Redis: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) cleanPersistedGames(ctx context.Context) error {
	records, err := s.redisStore.LoadGames(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		// Room cleanup stops timers before unregistering the session. Do not
		// remove its CAS predecessor while a live callback can still commit.
		if s.handler.GetGameSession(record.RoomCode) != nil {
			continue
		}
		expires := record.ExpiresAt
		if expires.IsZero() {
			snapshot, err := session.DecodeSnapshot(record)
			if err != nil {
				return err
			}
			expires = record.SavedAt.Add(snapshot.Config.OfflineRoomRetentionDuration())
		}
		if !expires.After(time.Now()) {
			if err := s.redisStore.DeleteExpiredGame(ctx, record); err != nil {
				return err
			}
		}
	}
	return nil
}

// SuspendRecovery stops process-owned work without changing durable game state.
func (s *Server) SuspendRecovery() {
	if s.recoveryCancel != nil {
		s.recoveryCancel()
	}
	for _, gs := range s.handler.GameSessions() {
		gs.Suspend()
	}
}
