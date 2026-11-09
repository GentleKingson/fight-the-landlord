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
		if cookie, err := r.Cookie(identityCookie); err == nil {
			id, token, _ := strings.Cut(cookie.Value, ".")
			identity, err := s.redisStore.VerifyIdentity(ctx, id, token)
			if err == nil {
				return identity, "", nil, nil
			}
			if !errors.Is(err, storage.ErrInvalidIdentity) {
				return nil, "", nil, err
			}
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
