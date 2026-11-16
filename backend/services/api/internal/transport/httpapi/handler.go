package httpapi

import (
	"ai-bud/services/api/internal/access/adapters/tickethttp"
	"ai-bud/services/api/internal/access/application"
	"ai-bud/services/api/internal/access/domain"
	conversation "ai-bud/services/api/internal/conversation/application"
	"ai-bud/services/api/internal/identity"
	identitydemo "ai-bud/services/api/internal/identity/demo"
	"ai-bud/services/api/internal/onboarding"
	"ai-bud/services/api/internal/ragclient"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type Authenticator interface {
	Authenticate(*http.Request) (identity.Actor, bool)
}

type DemoAuth struct{}

func (DemoAuth) Authenticate(r *http.Request) (identity.Actor, bool) {
	var id string
	switch r.Header.Get("Authorization") {
	case "Bearer demo-alex":
		id = "alex"
	case "Bearer demo-sam":
		id = "sam"
	case "Bearer demo-intern-platform":
		id = "intern-platform"
	case "Bearer demo-intern-search":
		id = "intern-search"
	case "Bearer demo-mentor-platform":
		id = "mentor-platform"
	default:
		return identity.Actor{}, false
	}
	a, err := (identitydemo.Directory{}).Resolve(r.Context(), id)
	return a, err == nil
}

type actorKey struct{}
type Handler struct {
	service      *application.Service
	logger       *slog.Logger
	rag          ragclient.Gateway
	conversation *conversation.Service
}

func New(service *application.Service, auth Authenticator, ping func(context.Context) error, logger *slog.Logger, options ...func(*Handler)) http.Handler {
	h := &Handler{service: service, logger: logger}
	for _, option := range options {
		option(h)
	}
	api := http.NewServeMux()
	api.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		a := actor(r)
		write(w, 200, map[string]string{"id": a.ID, "team": a.Team, "role": a.Role})
	})
	api.HandleFunc("POST /api/access-requests", h.propose)
	api.HandleFunc("GET /api/access-requests", h.list)
	api.HandleFunc("GET /api/access-requests/{id}", h.get)
	api.HandleFunc("POST /api/access-requests/{id}/confirm", h.confirm)
	api.HandleFunc("POST /api/access-requests/{id}/cancel", h.cancel)
	api.HandleFunc("POST /api/access-requests/{id}/refresh", h.refresh)
	api.HandleFunc("GET /api/onboarding", h.progress)
	api.HandleFunc("POST /api/rag/answers", h.answer)
	api.HandleFunc("POST /api/rag/search", h.search)
	api.HandleFunc("GET /api/conversation", h.currentConversation)
	api.HandleFunc("POST /api/conversation/new", h.newConversation)
	api.HandleFunc("POST /api/conversations/{id}/messages", h.sendMessage)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { write(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if ping(ctx) != nil {
			write(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		write(w, 200, map[string]string{"status": "ready"})
	})
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := auth.Authenticate(r)
		if !ok {
			writeError(w, 401, "unauthorized")
			return
		}
		timeout := 10 * time.Second
		if r.URL.Path == "/api/rag/answers" || r.URL.Path == "/api/rag/search" || strings.HasPrefix(r.URL.Path, "/api/conversations/") {
			timeout = ragclient.Timeout + 5*time.Second
		}
		ctx, cancel := context.WithTimeout(context.WithValue(r.Context(), actorKey{}, a), timeout)
		defer cancel()
		w.Header().Set("Cache-Control", "no-store")
		api.ServeHTTP(w, r.WithContext(ctx))
	}))
	return mux
}
func actor(r *http.Request) identity.Actor { return r.Context().Value(actorKey{}).(identity.Actor) }

type proposalInput struct {
	Resource string `json:"resource"`
	Access   string `json:"access"`
	Reason   string `json:"reason"`
}
type versionInput struct {
	Version int64 `json:"version"`
}
type requestView struct {
	ID        string             `json:"id"`
	Resource  string             `json:"resource"`
	Access    string             `json:"access"`
	Reason    string             `json:"reason"`
	State     domain.State       `json:"state"`
	Version   int64              `json:"version"`
	Ticket    *tickethttp.Result `json:"ticket,omitempty"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
}

func view(r domain.Request) requestView {
	v := requestView{ID: r.ID, Resource: r.Parameters.Resource, Access: r.Parameters.Access, Reason: r.Parameters.Reason, State: r.State, Version: r.Version, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if r.Ticket.ID != "" {
		v.Ticket = &tickethttp.Result{ID: r.Ticket.ID, Status: r.Ticket.Status}
	}
	return v
}
func read(w http.ResponseWriter, r *http.Request, dst any) error {
	return readLimit(w, r, dst, 8192)
}
func readLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		return domain.ErrInvalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return domain.ErrInvalid
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return domain.ErrInvalid
	}
	return nil
}
func (h *Handler) propose(w http.ResponseWriter, r *http.Request) {
	var in proposalInput
	if err := read(w, r, &in); err != nil {
		h.fail(w, err)
		return
	}
	v, err := h.service.Propose(r.Context(), actor(r), r.Header.Get("Idempotency-Key"), domain.Parameters{Resource: in.Resource, Access: in.Access, Reason: in.Reason})
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Location", "/api/access-requests/"+v.ID)
	write(w, 201, view(v))
}
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	v, err := h.service.Get(r.Context(), actor(r), r.PathValue("id"))
	h.result(w, v, err)
}
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	rows, err := h.service.List(r.Context(), actor(r))
	if err != nil {
		h.fail(w, err)
		return
	}
	out := make([]requestView, 0, len(rows))
	for _, v := range rows {
		out = append(out, view(v))
	}
	write(w, 200, map[string]any{"items": out})
}
func (h *Handler) confirm(w http.ResponseWriter, r *http.Request) {
	var in versionInput
	if err := read(w, r, &in); err != nil {
		h.fail(w, err)
		return
	}
	v, err := h.service.Confirm(r.Context(), actor(r), r.PathValue("id"), in.Version)
	h.result(w, v, err)
}
func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	var in versionInput
	if err := read(w, r, &in); err != nil {
		h.fail(w, err)
		return
	}
	v, err := h.service.Cancel(r.Context(), actor(r), r.PathValue("id"), in.Version)
	h.result(w, v, err)
}
func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	v, err := h.service.Refresh(r.Context(), actor(r), r.PathValue("id"))
	h.result(w, v, err)
}
func (h *Handler) progress(w http.ResponseWriter, r *http.Request) {
	rows, err := h.service.List(r.Context(), actor(r))
	if err != nil {
		h.fail(w, err)
		return
	}
	type item struct {
		RequestID string `json:"request_id"`
		Resource  string `json:"resource"`
		State     string `json:"state"`
		NextStep  string `json:"next_step"`
	}
	out := []item{}
	for _, r := range rows {
		p := onboarding.ForAccess(r)
		out = append(out, item{r.ID, r.Parameters.Resource, p.State, p.NextStep})
	}
	write(w, 200, map[string]any{"items": out})
}
func (h *Handler) result(w http.ResponseWriter, r domain.Request, err error) {
	if err != nil {
		h.fail(w, err)
		return
	}
	status := 200
	if r.State == domain.Submitting || r.State == domain.Unknown {
		status = 202
	}
	write(w, status, view(r))
}
func (h *Handler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalid):
		writeError(w, 400, "invalid_input")
	case errors.Is(err, domain.ErrForbidden):
		writeError(w, 403, "forbidden")
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, 404, "not_found")
	case errors.Is(err, domain.ErrConflict):
		writeError(w, 409, "conflict")
	case errors.Is(err, tickethttp.ErrUnavailable), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		writeError(w, 503, "temporarily_unavailable")
	default:
		h.logger.Error("request failed", "error", err)
		writeError(w, 500, "internal_error")
	}
}
func writeError(w http.ResponseWriter, status int, code string) {
	write(w, status, map[string]any{"error": map[string]string{"code": code}})
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
