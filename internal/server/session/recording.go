package session

// Training records are server-authoritative, seat-based, and never sent to clients.
// Each deal has its own file; only a successfully synced terminal record publishes
// the .jsonl file. A crash or I/O error leaves a .partial file for inspection.
import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/palemoky/fight-the-landlord/internal/game/card"
)

type gameRecording struct {
	file     *os.File
	id       string
	sequence int
}

func (gs *GameSession) startRecording() {
	if !gs.gameConfig.CollectTrainingData {
		return
	}
	// Pure bot games provide no human demonstrations.
	humans := 0
	players := make([]map[string]any, len(gs.players))
	for i, p := range gs.players {
		isBot := gs.room.Players[p.ID].Client.IsBot()
		if !isBot {
			humans++
		}
		players[i] = map[string]any{"seat": i, "is_bot": isBot, "hand": trainingCards(p.Hand)}
	}
	if humans == 0 {
		return
	}
	id := uuid.NewString()
	dir := filepath.Join(gs.gameConfig.TrainingDataDir, time.Now().UTC().Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("training data: %v", err)
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, id+".jsonl.partial"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("training data: %v", err)
		return
	}
	gs.recording = &gameRecording{file: f, id: id}
	gs.recordEvent("deal", map[string]any{"players": players, "bottom_cards": trainingCards(gs.bottomCards), "redeal_count": gs.redealCount, "card_encoding": "douzero-ranks-v1"})
}

func (gs *GameSession) recordEvent(kind string, data map[string]any) {
	r := gs.recording
	if r == nil {
		return
	}
	data["schema_version"] = 1
	data["game_id"] = r.id
	data["seq"] = r.sequence
	data["type"] = kind
	data["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
	if err := json.NewEncoder(r.file).Encode(data); err != nil {
		log.Printf("training data write failed (%s): %v", r.id, err)
		_ = r.file.Close()
		gs.recording = nil
		return
	}
	r.sequence++
}

func (gs *GameSession) finishRecording(status string, winner *GamePlayer) {
	if gs.recording == nil {
		return
	}
	data := map[string]any{"status": status}
	if winner != nil {
		landlordSeat := gs.landlordSeat()
		rewards, scores := make([]int, 3), make([]int, 3)
		for i, p := range gs.players {
			rewards[i] = -1
			if p.IsLandlord == winner.IsLandlord {
				rewards[i] = 1
			}
			scores[i] = rewards[i] * gs.finalMultiplier(winner)
			if p.IsLandlord {
				scores[i] *= 2
			}
		}
		data["winner_seat"], data["landlord_seat"] = winner.Seat, landlordSeat
		data["rewards"], data["scores"] = rewards, scores
		data["multiplier"] = gs.finalMultiplier(winner)
	}
	gs.recordEvent("end", data)
	r := gs.recording
	if r == nil {
		return
	}
	gs.recording = nil
	err := r.file.Sync()
	closeErr := r.file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(r.file.Name(), r.file.Name()[:len(r.file.Name())-len(".partial")])
	}
	if err != nil {
		log.Printf("training data finalize failed (%s): %v", r.id, err)
	}
}

func (gs *GameSession) landlordSeat() int {
	for i, p := range gs.players {
		if p.IsLandlord {
			return i
		}
	}
	return -1
}

func (gs *GameSession) recordDecision(kind string, p *GamePlayer, cards []card.Card, bid bool, source string) {
	if gs.recording == nil {
		return
	}
	if source == "" {
		source = "human"
		if gs.room.Players[p.ID].Client.IsBot() {
			source = "bot"
		}
	}
	counts := make([]int, len(gs.players))
	for i, other := range gs.players {
		counts[i] = len(other.Hand)
	}
	gs.recordEvent(kind, map[string]any{
		"seat": p.Seat, "source": source, "hand": trainingCards(p.Hand),
		"action": trainingCards(cards), "bid": bid, "is_grab": gs.landlordCaller != -1,
		"landlord_seat": gs.landlordSeat(), "num_cards_left": counts,
		"last_move": trainingCards(gs.lastPlayedHand.Cards), "last_seat": gs.lastPlayerIdx,
		"must_play":  gs.lastPlayerIdx == p.Seat || gs.lastPlayedHand.IsEmpty(),
		"bomb_count": gs.bombCount, "bid_multiplier": gs.bidMultiplier,
		"elapsed_ms": time.Since(gs.decisionStarted).Milliseconds(),
	})
}

func trainingCards(cards []card.Card) []int {
	out := make([]int, len(cards))
	for i, c := range cards {
		switch c.Rank {
		case card.Rank2:
			out[i] = 17
		case card.RankBlackJoker:
			out[i] = 20
		case card.RankRedJoker:
			out[i] = 30
		default:
			out[i] = int(c.Rank)
		}
	}
	return out
}
