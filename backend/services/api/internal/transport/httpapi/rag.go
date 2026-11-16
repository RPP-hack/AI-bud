package httpapi

import (
	"ai-bud/services/api/internal/conversation/domain"
	"errors"
	"net/http"

	"ai-bud/services/api/internal/ragclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func WithRAG(gateway ragclient.Gateway) func(*Handler) {
	return func(h *Handler) { h.rag = gateway }
}

func (h *Handler) answer(w http.ResponseWriter, r *http.Request) {
	var in ragclient.Input
	if err := readLimit(w, r, &in, 65536); err != nil {
		h.fail(w, err)
		return
	}
	if err := in.Validate(); err != nil {
		h.ragError(w, err)
		return
	}
	if h.rag == nil {
		writeError(w, 503, "temporarily_unavailable")
		return
	}
	result, err := h.rag.Answer(r.Context(), actor(r), in)
	if err != nil {
		h.ragError(w, err)
		return
	}
	write(w, 200, result)
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Query string  `json:"query"`
		TopK  *uint32 `json:"top_k"`
	}
	if err := readLimit(w, r, &in, 65536); err != nil {
		h.fail(w, err)
		return
	}
	query := ragclient.Input{Question: in.Query, TopK: in.TopK}
	if err := query.Validate(); err != nil {
		h.ragError(w, err)
		return
	}
	if h.rag == nil {
		writeError(w, 503, "temporarily_unavailable")
		return
	}
	result, err := h.rag.Search(r.Context(), actor(r), query)
	if err != nil {
		h.ragError(w, err)
		return
	}
	write(w, 200, result)
}

func (h *Handler) ragError(w http.ResponseWriter, err error) {
	if errors.Is(err, domain.ErrInvalid) {
		writeError(w, 400, "invalid_input")
		return
	}
	if failure := domain.DependencyFailure(status.Convert(err).Message()); failure != domain.ErrUnavailable {
		code := 503
		switch status.Code(err) {
		case codes.DeadlineExceeded:
			code = 504
		case codes.ResourceExhausted:
			code = 429
		case codes.DataLoss:
			code = 502
		}
		writeError(w, code, failure.Error())
		return
	}
	switch status.Code(err) {
	case codes.InvalidArgument:
		writeError(w, 400, "invalid_input")
	case codes.ResourceExhausted:
		writeError(w, 429, "resource_exhausted")
	case codes.DeadlineExceeded:
		writeError(w, 504, "deadline_exceeded")
	case codes.Canceled:
		writeError(w, 408, "request_cancelled")
	case codes.Unavailable, codes.Unauthenticated, codes.PermissionDenied:
		// These credentials belong to Go, not the authenticated end user.
		writeError(w, 503, "temporarily_unavailable")
	default:
		h.logger.Error("RAG call failed", "code", status.Code(err).String())
		writeError(w, 500, "internal_error")
	}
}
