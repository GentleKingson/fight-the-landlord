package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/palemoky/fight-the-landlord/internal/server/storage"
)

const identityCookie = "ddz_guest"

func (s *Server) authenticateTerminal(id, token string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	identity, err := s.redisStore.VerifyIdentity(ctx, id, token)
	if err != nil {
		return "", err
	}
	return identity.Name, nil
}

// resolveIdentity runs before upgrading. Redis failures never replace a valid cookie
// or silently create a temporary in-memory identity.
func (s *Server) resolveIdentity(r *http.Request) (*storage.GuestIdentity, string, http.Header, error) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	browser := r.Header.Get("Origin") != ""
	if browser {
		identity, err := s.verifyCookieIdentity(ctx, r)
		if err != nil || identity != nil {
			return identity, "", nil, err
		}
	}
	identity, token, err := s.redisStore.CreateIdentity(ctx, GenerateNickname(), s.config.Server.IdentityTTLDuration())
	if err != nil {
		return nil, "", nil, err
	}
	headers := http.Header{}
	if browser {
		cookie := &http.Cookie{Name: identityCookie, Value: identity.ID + "." + token, Path: "/", HttpOnly: true,
			SameSite: http.SameSiteStrictMode, Secure: s.config.Server.CookieSecure || r.TLS != nil,
			MaxAge: int(s.config.Server.IdentityTTLDuration() / time.Second), Expires: identity.ExpiresAt}
		headers.Add("Set-Cookie", cookie.String())
		token = "" // Never expose a browser bearer credential to JavaScript.
	}
	return identity, token, headers, nil
}

// verifyCookieIdentity returns no identity and no error when a new guest is needed.
// Storage failures propagate so an outage cannot replace an existing identity.
func (s *Server) verifyCookieIdentity(ctx context.Context, r *http.Request) (*storage.GuestIdentity, error) {
	cookie, err := r.Cookie(identityCookie)
	if errors.Is(err, http.ErrNoCookie) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	id, token, _ := strings.Cut(cookie.Value, ".")
	identity, err := s.redisStore.VerifyIdentity(ctx, id, token)
	if errors.Is(err, storage.ErrInvalidIdentity) {
		return nil, nil
	}
	return identity, err
}
