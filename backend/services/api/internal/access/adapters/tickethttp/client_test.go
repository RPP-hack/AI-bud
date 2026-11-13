package tickethttp

import (
	"ai-bud/services/api/internal/access/domain"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientContract(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
	}{{"ok", `{"id":"T-1","status":"pending"}`, 200, nil}, {"missing", "", 404, domain.ErrNotFound}, {"server", "", 500, ErrUnavailable}, {"invalid JSON", "no", 200, ErrUnavailable}, {"invalid status", `{"id":"T-1","status":"done"}`, 200, ErrUnavailable}, {"extra fields", `{"id":"T-1","status":"pending","extra":true}`, 200, ErrUnavailable}, {"two values", `{"id":"T-1","status":"pending"}{}`, 200, ErrUnavailable}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer token" {
					t.Error("token missing")
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := New(srv.URL, "token", srv.Client())
			_, err := c.Lookup(context.Background(), "op")
			if !errors.Is(err, tc.want) {
				t.Fatalf("%v want %v", err, tc.want)
			}
			_, err = c.Create(context.Background(), "op", "user", domain.Parameters{Resource: "repo", Access: "read", Reason: "why"})
			if !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
		})
	}
	c := New("://", "token", http.DefaultClient)
	if _, err := c.Lookup(context.Background(), "op"); err == nil {
		t.Fatal("bad URL")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c = New("http://127.0.0.1:1", "token", http.DefaultClient)
	if _, err := c.Lookup(ctx, "op"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
