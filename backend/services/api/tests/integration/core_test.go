//go:build integration

package integration_test

import (
	"ai-bud/services/api/internal/access/adapters/demo"
	storage "ai-bud/services/api/internal/access/adapters/postgres"
	"ai-bud/services/api/internal/access/adapters/tickethttp"
	"ai-bud/services/api/internal/access/application"
	"ai-bud/services/api/internal/access/domain"
	"ai-bud/services/api/internal/identity"
	"ai-bud/services/api/internal/mocktickets"
	database "ai-bud/services/api/internal/platform/postgres"
	"ai-bud/services/api/internal/transport/httpapi"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := emptyTestDB(t)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := database.Migrate(context.Background(), pool); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	return pool
}

func emptyTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("integration tests require TEST_DATABASE_URL; use the local Compose PostgreSQL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("buddy_test_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		if err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
func TestPostgresAtomicityAndConcurrency(t *testing.T) {
	pool := testDB(t)
	repo := storage.New(pool)
	ctx := context.Background()
	p := domain.Parameters{Resource: "notifications", Access: "read", Reason: "first task"}
	r, _ := domain.New("operation", "alex", "key", p, time.Now().UTC())
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			candidate := r
			candidate.ID = fmt.Sprintf("candidate-%d", i)
			got, err := repo.Insert(ctx, candidate)
			if err != nil || got.IdempotencyKey != "key" {
				t.Errorf("insert: %v %v", got, err)
			}
		}(i)
	}
	wg.Wait()
	rows, err := repo.List(ctx, "alex")
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	r = rows[0]
	bad := r
	bad.Parameters.Reason = "different"
	if _, err = repo.Insert(ctx, bad); err != domain.ErrConflict {
		t.Fatal(err)
	}
	if _, err = repo.Get(ctx, "sam", r.ID); err != domain.ErrNotFound {
		t.Fatal(err)
	}
	if _, err = repo.Update(ctx, "sam", r.ID, func(*domain.Request) error { return nil }); err != domain.ErrNotFound {
		t.Fatal(err)
	}
	if _, err = repo.Update(ctx, "alex", r.ID, func(r *domain.Request) error { r.State = domain.Cancelled; return domain.ErrConflict }); err != domain.ErrConflict {
		t.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.Update(ctx, "alex", r.ID, func(v *domain.Request) error {
				if v.State == domain.Submitting {
					return nil
				}
				return v.Confirm(1)
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var count int
	pool.QueryRow(ctx, "SELECT count(*) FROM access_events WHERE request_id=$1", r.ID).Scan(&count)
	if count != 2 {
		t.Fatal("audit count", count)
	}
	got, err := storage.New(pool).Get(ctx, "alex", r.ID)
	if err != nil || got.Version != 2 || got.State != domain.Submitting {
		t.Fatal(got, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = repo.List(cancelled, "alex"); err == nil {
		t.Fatal("cancelled query")
	}
	if _, err = repo.Get(cancelled, "alex", r.ID); err == nil {
		t.Fatal("cancelled query")
	}
	if _, err = repo.Insert(cancelled, r); err == nil {
		t.Fatal("cancelled insert")
	}
	if _, err = repo.Update(cancelled, "alex", r.ID, func(*domain.Request) error { return nil }); err == nil {
		t.Fatal("cancelled update")
	}
}

func request(t *testing.T, handler http.Handler, method, path, body, token string) (int, []byte) {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "e2e-key")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}
func TestHTTPPostgresAndMockEndToEnd(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	mock := httptest.NewServer(mocktickets.New(pool, "test-token", time.Hour))
	defer mock.Close()
	client := tickethttp.New(mock.URL, "test-token", mock.Client())
	repo := storage.New(pool)
	svc := application.New(repo, client, demo.Policy{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := httpapi.New(svc, httpapi.DemoAuth{}, pool.Ping, logger)
	code, data := request(t, api, "POST", "/api/access-requests", `{"resource":"notifications","access":"read","reason":"first task"}`, "demo-alex")
	if code != 201 {
		t.Fatal(code, string(data))
	}
	var proposed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &proposed); err != nil {
		t.Fatal(err)
	}
	base := "/api/access-requests/" + proposed.ID
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _ := request(t, api, "POST", base+"/confirm", `{"version":1}`, "demo-alex")
			if code != 200 && code != 202 {
				t.Error(code)
			}
		}()
	}
	wg.Wait()
	var count int
	pool.QueryRow(ctx, "SELECT count(*) FROM mock_access.tickets").Scan(&count)
	if count != 1 {
		t.Fatal("duplicate external tickets", count)
	}
	api = httpapi.New(application.New(storage.New(pool), client, demo.Policy{}), httpapi.DemoAuth{}, pool.Ping, logger)
	code, data = request(t, api, "GET", base, "", "demo-alex")
	if code != 200 || !strings.Contains(string(data), "pending") {
		t.Fatal(code, string(data))
	}
	code, _ = request(t, api, "GET", base, "", "demo-sam")
	if code != 404 {
		t.Fatal(code)
	}
	if _, err := pool.Exec(ctx, "UPDATE mock_access.tickets SET created_at=now()-interval '2 hours'"); err != nil {
		t.Fatal(err)
	}
	code, data = request(t, api, "POST", base+"/refresh", "", "demo-alex")
	if code != 200 || !strings.Contains(string(data), "granted") {
		t.Fatal(code, string(data))
	}
	code, data = request(t, api, "GET", "/api/onboarding", "", "demo-alex")
	if code != 200 || !strings.Contains(string(data), "done") {
		t.Fatal(code, string(data))
	}
	a := identity.Actor{ID: "alex", Team: "notifications"}
	r, err := svc.Propose(ctx, a, "crash-key", domain.Parameters{Resource: "notifications", Access: "read", Reason: "restart"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Update(ctx, a.ID, r.ID, func(r *domain.Request) error { return r.Confirm(1) })
	if err != nil {
		t.Fatal(err)
	}
	r, err = svc.Refresh(ctx, a, r.ID)
	if err != nil || r.State != domain.Submitted {
		t.Fatal(r, err)
	}
}

func TestMockContractAndDurability(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	handler := mocktickets.New(pool, "token", 0)
	srv := httptest.NewServer(handler)
	defer srv.Close()
	client := tickethttp.New(srv.URL, "token", srv.Client())
	p := domain.Parameters{Resource: "notifications", Access: "read", Reason: "task"}
	if _, err := client.Lookup(ctx, "missing"); err != domain.ErrNotFound {
		t.Fatal(err)
	}
	first, err := client.Create(ctx, "operation", "alex", p)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Create(ctx, "operation", "alex", p)
	if err != nil || first != second {
		t.Fatal(second, err)
	}
	if _, err = client.Create(ctx, "operation", "sam", p); !errors.Is(err, tickethttp.ErrUnavailable) {
		t.Fatal(err)
	}
	fresh := httptest.NewServer(mocktickets.New(pool, "token", 0))
	defer fresh.Close()
	found, err := tickethttp.New(fresh.URL, "token", fresh.Client()).Lookup(ctx, "operation")
	if err != nil || found != first {
		t.Fatal(found, err)
	}
	for _, tc := range []struct {
		method, path, body, token string
		status                    int
	}{{"GET", "/healthz", "", "", 200}, {"GET", "/tickets/operation", "", "bad", 401}, {"POST", "/tickets", "{", "token", 400}, {"POST", "/tickets", "{} {}", "token", 400}, {"POST", "/tickets", "{}", "token", 400}} {
		code, _ := request(t, handler, tc.method, tc.path, tc.body, tc.token)
		if code != tc.status {
			t.Fatal(tc, code)
		}
	}
}
