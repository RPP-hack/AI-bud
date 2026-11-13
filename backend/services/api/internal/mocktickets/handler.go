package mocktickets

import (
	"ai-bud/services/api/internal/access/adapters/tickethttp"
	"ai-bud/services/api/internal/access/domain"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	pool       *pgxpool.Pool
	token      string
	grantAfter time.Duration
}

func New(pool *pgxpool.Pool, token string, grantAfter time.Duration) http.Handler {
	h := &Handler{pool, token, grantAfter}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /tickets", h.create)
	mux.HandleFunc("GET /tickets/{id}", h.get)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && (token == "" || r.Header.Get("Authorization") != "Bearer "+token) {
			http.Error(w, "unauthorized", 401)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var in tickethttp.CreateInput
	if dec.Decode(&in) != nil {
		http.Error(w, "invalid input", 400)
		return
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		http.Error(w, "invalid input", 400)
		return
	}
	p := domain.Parameters{Resource: in.Resource, Access: in.Access, Reason: in.Reason}
	if p.Validate() != nil || in.OperationID == "" || len(in.OperationID) > 128 || in.OwnerID == "" || len(in.OwnerID) > 128 {
		http.Error(w, "invalid input", 400)
		return
	}
	data, _ := json.Marshal(p)
	_, err := h.pool.Exec(r.Context(), "INSERT INTO mock_access.tickets(operation_id,owner_id,parameters,ticket_id,status) VALUES($1,$2,$3,$4,'pending') ON CONFLICT(operation_id) DO NOTHING", in.OperationID, in.OwnerID, data, "REQ-"+in.OperationID)
	if err != nil {
		http.Error(w, "storage unavailable", 503)
		return
	}
	var owner string
	var oldData []byte
	err = h.pool.QueryRow(r.Context(), "SELECT owner_id,parameters FROM mock_access.tickets WHERE operation_id=$1", in.OperationID).Scan(&owner, &oldData)
	var old domain.Parameters
	if err != nil || json.Unmarshal(oldData, &old) != nil {
		http.Error(w, "storage unavailable", 503)
		return
	}
	if owner != in.OwnerID || old != p {
		http.Error(w, "idempotency conflict", 409)
		return
	}
	h.reply(w, r.Context(), in.OperationID)
}
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	h.reply(w, r.Context(), r.PathValue("id"))
}
func (h *Handler) reply(w http.ResponseWriter, ctx context.Context, id string) {
	if h.grantAfter > 0 {
		_, err := h.pool.Exec(ctx, "UPDATE mock_access.tickets SET status='granted' WHERE operation_id=$1 AND status='pending' AND created_at <= $2", id, time.Now().Add(-h.grantAfter))
		if err != nil {
			http.Error(w, "storage unavailable", 503)
			return
		}
	}
	var out tickethttp.Result
	err := h.pool.QueryRow(ctx, "SELECT ticket_id,status FROM mock_access.tickets WHERE operation_id=$1", id).Scan(&out.ID, &out.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "not found", 404)
		return
	}
	if err != nil {
		http.Error(w, "storage unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
