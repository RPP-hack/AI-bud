package httpapi

import (
	"ai-bud/services/api/internal/access/adapters/demo"
	"ai-bud/services/api/internal/access/adapters/memory"
	"ai-bud/services/api/internal/access/adapters/tickethttp"
	"ai-bud/services/api/internal/access/application"
	"ai-bud/services/api/internal/access/domain"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type gateway struct{ fail bool }

func (g *gateway) Create(context.Context, string, string, domain.Parameters) (domain.Ticket, error) {
	if g.fail {
		return domain.Ticket{}, tickethttp.ErrUnavailable
	}
	return domain.Ticket{ID: "ticket", Status: domain.Pending}, nil
}
func (g *gateway) Lookup(context.Context, string) (domain.Ticket, error) {
	return domain.Ticket{ID: "ticket", Status: domain.Granted}, nil
}
func call(h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Idempotency-Key", "key")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestHTTPWorkflow(t *testing.T) {
	svc := application.New(memory.New(), &gateway{}, demo.Policy{})
	h := New(svc, DemoAuth{}, func(context.Context) error { return nil }, slog.Default())
	for _, path := range []string{"/healthz", "/readyz", "/api/me", "/api/onboarding", "/api/access-requests"} {
		if w := call(h, "GET", path, "", "demo-alex"); w.Code != 200 {
			t.Fatal(path, w.Code)
		}
	}
	if w := call(h, "GET", "/api/me", "", "demo-sam"); w.Code != 200 || !strings.Contains(w.Body.String(), "payments") {
		t.Fatal(w)
	}
	w := call(h, "POST", "/api/access-requests", `{"resource":"notifications","access":"read","reason":"first task"}`, "demo-alex")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v requestView
	json.Unmarshal(w.Body.Bytes(), &v)
	base := "/api/access-requests/" + v.ID
	if w = call(h, "GET", base, "", "demo-sam"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w = call(h, "GET", base, "", "demo-alex"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = call(h, "POST", base+"/confirm", `{"version":2}`, "demo-alex"); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w = call(h, "POST", base+"/confirm", `{"version":1}`, "demo-alex"); w.Code != 200 || !strings.Contains(w.Body.String(), "pending") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(h, "POST", base+"/refresh", "", "demo-alex"); w.Code != 200 || !strings.Contains(w.Body.String(), "granted") {
		t.Fatal(w)
	}
	if w = call(h, "GET", "/api/onboarding", "", "demo-alex"); !strings.Contains(w.Body.String(), "done") {
		t.Fatal(w)
	}
	if w = call(h, "GET", "/api/access-requests", "", "demo-alex"); !strings.Contains(w.Body.String(), v.ID) {
		t.Fatal(w)
	}
}
func TestHTTPValidationAndCancellation(t *testing.T) {
	h := New(application.New(memory.New(), &gateway{}, demo.Policy{}), DemoAuth{}, func(context.Context) error { return errors.New("offline") }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, tc := range []struct {
		method, path, body, token string
		want                      int
	}{
		{"GET", "/readyz", "", "", 503}, {"GET", "/api/me", "", "", 401},
		{"POST", "/api/access-requests", `{"resource":"payments","access":"read","reason":"task"}`, "demo-alex", 403},
		{"POST", "/api/access-requests", `{"resource":"notifications","access":"write","reason":"task"}`, "demo-alex", 403},
		{"POST", "/api/access-requests", `{"user_id":"sam"}`, "demo-alex", 400},
		{"POST", "/api/access-requests", `{} {}`, "demo-alex", 400},
		{"POST", "/api/access-requests", strings.Repeat("x", 9000), "demo-alex", 400},
		{"POST", "/api/access-requests/id/confirm", `{`, "demo-alex", 400},
		{"POST", "/api/access-requests/id/cancel", `{`, "demo-alex", 400},
	} {
		w := call(h, tc.method, tc.path, tc.body, tc.token)
		if w.Code != tc.want {
			t.Fatalf("%+v got %d", tc, w.Code)
		}
	}
	w := call(h, "POST", "/api/access-requests", `{"resource":"notifications","access":"read","reason":"task"}`, "demo-alex")
	var v requestView
	json.Unmarshal(w.Body.Bytes(), &v)
	if w = call(h, "POST", "/api/access-requests/"+v.ID+"/cancel", `{"version":1}`, "demo-alex"); w.Code != 200 || !strings.Contains(w.Body.String(), "cancelled") {
		t.Fatal(w)
	}
	r := httptest.NewRequest("POST", "/api/access-requests", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer demo-alex")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	h2 := New(application.New(memory.New(), &gateway{fail: true}, demo.Policy{}), DemoAuth{}, func(context.Context) error { return nil }, slog.Default())
	w = call(h2, "POST", "/api/access-requests", `{"resource":"notifications","access":"read","reason":"task"}`, "demo-alex")
	json.Unmarshal(w.Body.Bytes(), &v)
	if w = call(h2, "POST", "/api/access-requests/"+v.ID+"/confirm", `{"version":1}`, "demo-alex"); w.Code != 202 || !strings.Contains(w.Body.String(), "unknown") {
		t.Fatal(w)
	}
}
func TestErrorsDoNotExposeInternals(t *testing.T) {
	h := &Handler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, tc := range []struct {
		err    error
		status int
	}{{tickethttp.ErrUnavailable, 503}, {context.DeadlineExceeded, 503}, {context.Canceled, 503}, {errors.New("secret database address"), 500}} {
		w := httptest.NewRecorder()
		h.fail(w, tc.err)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "secret") {
			t.Fatal(w)
		}
	}
}
