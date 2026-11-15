package httpapi

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"ai-bud/services/api/internal/identity"
	"ai-bud/services/api/internal/ragclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ragGateway struct {
	actor   identity.Actor
	calls   int
	err     error
	timeout time.Duration
}

func (g *ragGateway) Answer(ctx context.Context, a identity.Actor, in ragclient.Input) (ragclient.Answer, error) {
	g.actor = a
	g.calls++
	deadline, _ := ctx.Deadline()
	g.timeout = time.Until(deadline)
	return ragclient.Answer{Status: "insufficient_evidence", Steps: []ragclient.Step{}, Sources: map[string]ragclient.Source{}}, g.err
}
func (g *ragGateway) Search(ctx context.Context, a identity.Actor, in ragclient.Input) (ragclient.SearchResult, error) {
	g.actor = a
	g.calls++
	return ragclient.SearchResult{Sources: []ragclient.Source{}}, g.err
}

func TestRAGRoutesUseServerIdentity(t *testing.T) {
	g := &ragGateway{}
	h := New(nil, DemoAuth{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), WithRAG(g))
	w := call(h, "POST", "/api/rag/answers", `{"question":"Jira 403"}`, "demo-intern-platform")
	if w.Code != 200 || g.actor.Role != "intern" || g.actor.Team != "platform" || g.timeout < 30*time.Second {
		t.Fatal(w, g)
	}
	for _, body := range []string{`{"question":"Jira","actor":{"role":"mentor"}}`, `{"question":"Jira","user_id":"other"}`, `{"question":"Jira","top_k":0}`, `{"question":"Jira","mode":"bad"}`} {
		if w := call(h, "POST", "/api/rag/answers", body, "demo-intern-platform"); w.Code != 400 {
			t.Fatal(w)
		}
	}
	if g.calls != 1 {
		t.Fatal("invalid requests reached RAG")
	}
	if w := call(h, "POST", "/api/rag/search", `{"query":"SEARCH-730"}`, "demo-intern-search"); w.Code != 200 || g.actor.Team != "search" {
		t.Fatal(w)
	}
	if w := call(h, "POST", "/api/rag/answers", `{"question":"Jira"}`, "bad"); w.Code != 401 {
		t.Fatal(w)
	}
	for _, tc := range []struct {
		code codes.Code
		want int
	}{{codes.Unavailable, 503}, {codes.Unauthenticated, 503}, {codes.DeadlineExceeded, 504}, {codes.ResourceExhausted, 429}, {codes.Internal, 500}, {codes.InvalidArgument, 400}} {
		g.err = status.Error(tc.code, "private details")
		w := call(h, "POST", "/api/rag/answers", `{"question":"Jira"}`, "demo-intern-platform")
		if w.Code != tc.want {
			t.Fatal(tc, w)
		}
	}
}
