package server

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func FuzzWebSessionOriginAndTrustedProxy(f *testing.F) {
	f.Add("https://game.example", "10.0.0.4:443", "https", false)
	f.Add("", "203.0.113.8:443", "https", false)
	f.Add("https://evil.example", "203.0.113.8:443", "HTTPS", false)
	f.Add(" https://game.example ", "invalid remote", "https,http", true)

	checker := NewOriginChecker([]string{"https://game.example"})
	resolver, err := NewClientIPResolver([]string{"10.0.0.0/8"})
	if err != nil {
		f.Fatalf("create trusted proxy resolver: %v", err)
	}

	f.Fuzz(func(t *testing.T, origin, remoteAddr, forwardedProto string, directTLS bool) {
		if len(origin) > 4096 || len(remoteAddr) > 4096 || len(forwardedProto) > 4096 {
			t.Skip()
		}
		request := httptest.NewRequest(http.MethodPost, "http://game.example/session/revoke", http.NoBody)
		request.Header.Set("Origin", origin)
		request.Header.Set("X-Forwarded-Proto", forwardedProto)
		request.RemoteAddr = remoteAddr
		if directTLS {
			request.TLS = &tls.ConnectionState{}
		}

		originAllowed := browserOriginAllowed(checker, request)
		if originAllowed && !strings.EqualFold(origin, "https://game.example") {
			t.Fatalf("unexpected browser origin accepted: %q", origin)
		}

		secure := requestUsesHTTPS(request, resolver)
		if directTLS && !secure {
			t.Fatal("direct TLS request was not recognized as secure")
		}
		if secure && !directTLS {
			if forwardedProto != "https" {
				t.Fatalf("non-exact forwarded proto was trusted: %q", forwardedProto)
			}
			_, remoteIP := remoteClientIP(remoteAddr)
			if !remoteIP.IsValid() || !resolver.isTrusted(remoteIP) {
				t.Fatalf("untrusted direct peer was allowed to assert HTTPS: %q", remoteAddr)
			}
		}
	})
}

func FuzzSessionRevokeJSON(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"token":"must-not-be-read"}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{"padding":"` + strings.Repeat("x", 2048) + `"}`))
	f.Add([]byte{0xff, 0x00, '{', '}'})

	server := &Server{originChecker: NewOriginChecker([]string{"https://game.example"})}
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > 8192 {
			t.Skip()
		}
		request := httptest.NewRequest(http.MethodPost, "https://game.example/session/revoke", strings.NewReader(string(body)))
		request.Header.Set("Origin", "https://game.example")
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()

		server.handleSessionRevoke(recorder, request)

		if recorder.Code != http.StatusNoContent && recorder.Code != http.StatusBadRequest {
			t.Fatalf("unexpected revoke status %d", recorder.Code)
		}
		if recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("revoke response omitted Cache-Control: no-store")
		}
	})
}
