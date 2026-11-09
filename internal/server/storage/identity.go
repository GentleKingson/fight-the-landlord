package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var ErrInvalidIdentity = errors.New("identity expired or invalid")

// GuestIdentity contains verification material, never the bearer credential.
type GuestIdentity struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TokenHash string    `json:"token_hash"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (rs *RedisStore) CreateIdentity(ctx context.Context, name string, ttl time.Duration) (*GuestIdentity, string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, "", err
	}
	token := hex.EncodeToString(secret)
	hash := sha256.Sum256([]byte(token))
	identity := &GuestIdentity{ID: uuid.NewString(), Name: name, TokenHash: hex.EncodeToString(hash[:]), ExpiresAt: time.Now().Add(ttl)}
	data, err := json.Marshal(identity)
	if err != nil {
		return nil, "", err
	}
	if err := rs.client.Set(ctx, "identity:"+identity.ID, data, ttl).Err(); err != nil {
		return nil, "", err
	}
	return identity, token, nil
}

func (rs *RedisStore) VerifyIdentity(ctx context.Context, id, token string) (*GuestIdentity, error) {
	if _, err := uuid.Parse(id); err != nil || len(token) != 64 {
		return nil, ErrInvalidIdentity
	}
	data, err := rs.client.Get(ctx, "identity:"+id).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrInvalidIdentity
	}
	if err != nil {
		return nil, err
	}
	var identity GuestIdentity
	if err := json.Unmarshal(data, &identity); err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(token))
	expected, err := hex.DecodeString(identity.TokenHash)
	if err != nil || subtle.ConstantTimeCompare(expected, hash[:]) != 1 || identity.ID != id || !time.Now().Before(identity.ExpiresAt) {
		return nil, ErrInvalidIdentity
	}
	return &identity, nil
}
