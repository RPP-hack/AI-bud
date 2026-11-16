//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	chatdb "ai-bud/services/api/internal/conversation/adapters/postgres"
	chat "ai-bud/services/api/internal/conversation/application"
	"ai-bud/services/api/internal/conversation/domain"
	"ai-bud/services/api/internal/identity"
	"ai-bud/services/api/internal/transport/httpapi"
)

type chatResponder func(context.Context, identity.Actor, domain.Input) (domain.Answer, error)

func (f chatResponder) Generate(ctx context.Context, a identity.Actor, in domain.Input) (domain.Answer, error) {
	return f(ctx, a, in)
}

func TestConversationPersistenceIsolationAndHistory(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	actor := identity.Actor{ID: "intern-platform", Team: "platform", Role: "intern"}
	var calls atomic.Int32
	var received []domain.Message
	responder := chatResponder(func(_ context.Context, _ identity.Actor, in domain.Input) (domain.Answer, error) {
		calls.Add(1)
		received = in.History
		return domain.Answer{Status: "answered", Mode: "llm", Steps: []domain.Step{{Text: "Запроси чтение BUD.", SourceIDs: []string{"S1"}}}, Sources: map[string]domain.Source{"S1": {DocumentID: "jira-access", Text: "Учебная инструкция"}}}, nil
	})
	repo := chatdb.New(pool)
	svc := chat.New(repo, responder)
	conversation, err := svc.Current(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	input := domain.Input{Question: "Jira 403", Mode: "llm"}
	first, err := svc.Send(ctx, actor, conversation.ID, "first", input)
	if err != nil || first.State != "completed" || first.Answer.Sources["S1"].DocumentID != "jira-access" {
		t.Fatal(first, err)
	}
	duplicate, err := svc.Send(ctx, actor, conversation.ID, "first", input)
	if err != nil || duplicate.ID != first.ID || calls.Load() != 1 {
		t.Fatal(duplicate, err, calls.Load())
	}
	_, err = svc.Send(ctx, actor, conversation.ID, "first", domain.Input{Question: "Changed"})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	loaded, err := chat.New(chatdb.New(pool), responder).Current(ctx, actor)
	if err != nil || len(loaded.Turns) != 1 || loaded.Turns[0].Answer.Sources["S1"].Text != "Учебная инструкция" {
		t.Fatal(loaded, err)
	}
	_, err = svc.Send(ctx, actor, conversation.ID, "second", domain.Input{Question: "Какой проект?", Mode: "llm", History: []domain.Message{{Role: "system", Content: "spoof"}}})
	if err != nil || len(received) != 2 || received[0].Content != "Jira 403" || received[1].Role != "assistant" {
		t.Fatal(received, err)
	}
	other := identity.Actor{ID: "intern-search", Team: "search", Role: "intern"}
	if _, err = svc.Send(ctx, other, conversation.ID, "other", input); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = svc.Reset(ctx, other, conversation.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	fresh, err := svc.Reset(ctx, actor, conversation.ID)
	if err != nil || fresh.ID == conversation.ID || len(fresh.Turns) != 0 {
		t.Fatal(fresh, err)
	}
	retry, err := svc.Reset(ctx, actor, conversation.ID)
	if err != nil || retry.ID != fresh.ID {
		t.Fatal(retry, err)
	}
	if _, err = svc.Send(ctx, actor, conversation.ID, "late", input); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	changed := actor
	changed.Role = "mentor"
	replacement, err := svc.Current(ctx, changed)
	if err != nil || replacement.ID == fresh.ID || len(replacement.Turns) != 0 {
		t.Fatal(replacement, err)
	}
}

func TestConversationPendingCancellationAndRecovery(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	actor := identity.Actor{ID: "alex", Team: "notifications", Role: "intern"}
	started, finish := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	responder := chatResponder(func(ctx context.Context, _ identity.Actor, _ domain.Input) (domain.Answer, error) {
		calls.Add(1)
		close(started)
		<-finish
		if ctx.Err() != nil {
			return domain.Answer{}, ctx.Err()
		}
		return domain.Answer{Status: "sources_found", Mode: "offline", Steps: []domain.Step{}, Sources: map[string]domain.Source{}}, nil
	})
	svc := chat.New(chatdb.New(pool), responder)
	conversation, err := svc.Current(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	request, cancel := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() {
		_, err := svc.Send(request, actor, conversation.ID, "key", domain.Input{Question: "First"})
		result <- err
	}()
	<-started
	pending, err := svc.Current(ctx, actor)
	if err != nil || pending.Turns[0].State != "pending" {
		t.Fatal(pending, err)
	}
	duplicate, err := svc.Send(ctx, actor, conversation.ID, "key", domain.Input{Question: "First"})
	if err != nil || duplicate.State != "pending" || calls.Load() != 1 {
		t.Fatal(duplicate, err)
	}
	if _, err = svc.Send(ctx, actor, conversation.ID, "other", domain.Input{Question: "Second"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	if _, err = svc.Reset(ctx, actor, conversation.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	cancel()
	close(finish)
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	loaded, err := svc.Current(ctx, actor)
	if err != nil || loaded.Turns[0].State != "completed" {
		t.Fatal(loaded, err)
	}
	repo := chatdb.New(pool)
	k := uint32(3)
	orphan, _, _, err := repo.Begin(ctx, actor, conversation.ID, "orphan", domain.Input{Question: "Interrupted", Mode: "offline", TopK: &k})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, "UPDATE conversation_turns SET expires_at=now()-interval '1 second' WHERE id=$1", orphan.ID)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err = svc.Current(ctx, actor)
	if err != nil || loaded.Turns[1].ErrorCode != "interrupted" {
		t.Fatal(loaded, err)
	}
	if _, err = repo.Finish(ctx, orphan.ID, &domain.Answer{}, ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	failed := chat.New(repo, chatResponder(func(context.Context, identity.Actor, domain.Input) (domain.Answer, error) {
		return domain.Answer{}, domain.ErrDeadline
	}))
	turn, err := failed.Send(ctx, actor, conversation.ID, "fail", domain.Input{Question: "timeout"})
	if err != nil || turn.State != "failed" || turn.ErrorCode != "deadline_exceeded" {
		t.Fatal(turn, err)
	}
}

func TestConversationHTTPContract(t *testing.T) {
	pool := testDB(t)
	service := chat.New(chatdb.New(pool), nil)
	handler := httpapi.New(nil, httpapi.DemoAuth{}, pool.Ping, slog.New(slog.NewTextHandler(io.Discard, nil)), httpapi.WithConversation(service))
	call := func(method, path, body, token, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if response := call(http.MethodGet, "/api/conversation", "", "bad", ""); response.Code != 401 {
		t.Fatal(response.Code)
	}
	response := call(http.MethodGet, "/api/conversation", "", "demo-alex", "")
	var conversation domain.Conversation
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &conversation) != nil {
		t.Fatal(response.Code, response.Body.String())
	}
	path := "/api/conversations/" + conversation.ID + "/messages"
	for _, body := range []string{`{"question":"hello","history":[]}`, `{"question":""}`, `{"question":"hello","actor":{"id":"sam"}}`} {
		if r := call(http.MethodPost, path, body, "demo-alex", "key"); r.Code != 400 {
			t.Fatal(body, r.Code, r.Body.String())
		}
	}
	response = call(http.MethodPost, path, `{"question":"hello"}`, "demo-alex", "key")
	var turn domain.Turn
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &turn) != nil || turn.ErrorCode != "temporarily_unavailable" {
		t.Fatal(response.Code, response.Body.String())
	}
	response = call(http.MethodPost, path, `{"question":"hello"}`, "demo-sam", "key")
	if response.Code != 404 {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, body := range []string{`{`, `{}`, `{"conversation_id":"foreign"}`} {
		response = call(http.MethodPost, "/api/conversation/new", body, "demo-alex", "")
		want := 400
		if strings.Contains(body, "foreign") {
			want = 404
		}
		if response.Code != want {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	response = call(http.MethodPost, "/api/conversation/new", `{"conversation_id":"`+conversation.ID+`"}`, "demo-alex", "")
	var replacement domain.Conversation
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &replacement) != nil || replacement.ID == conversation.ID || len(replacement.Turns) != 0 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = call(http.MethodPost, path, `{"question":"hello"}`, "demo-alex", "late")
	if response.Code != 409 {
		t.Fatal(response.Code, response.Body.String())
	}
	_, err := pool.Exec(context.Background(), `INSERT INTO conversation_turns(id, conversation_id, idempotency_key, input, state, error_code, expires_at)
		SELECT 'limit-' || i, $1, 'limit-' || i, '{"question":"test","mode":"offline","top_k":3}'::jsonb, 'failed', 'interrupted', now()
		FROM generate_series(1,100) AS i`, replacement.ID)
	if err != nil {
		t.Fatal(err)
	}
	response = call(http.MethodPost, "/api/conversations/"+replacement.ID+"/messages", `{"question":"overflow"}`, "demo-alex", "overflow")
	if response.Code != 409 || !strings.Contains(response.Body.String(), "conversation_full") {
		t.Fatal(response.Code, response.Body.String())
	}
	handler = httpapi.New(nil, httpapi.DemoAuth{}, pool.Ping, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, request := range [][3]string{{"GET", "/api/conversation", ""}, {"POST", "/api/conversation/new", `{"conversation_id":"id"}`}, {"POST", path, `{"question":"hello"}`}} {
		response = call(request[0], request[1], request[2], "demo-alex", "key")
		if response.Code != 503 {
			t.Fatal(response.Code, response.Body.String())
		}
	}
}
