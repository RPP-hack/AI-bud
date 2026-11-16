package httpapi

import (
	"ai-bud/services/api/internal/conversation/application"
	"ai-bud/services/api/internal/conversation/domain"
	"errors"
	"net/http"
)

func WithConversation(service *application.Service) func(*Handler) {
	return func(h *Handler) { h.conversation = service }
}

func (h *Handler) currentConversation(w http.ResponseWriter, r *http.Request) {
	if h.conversation == nil {
		writeError(w, 503, "temporarily_unavailable")
		return
	}
	result, err := h.conversation.Current(r.Context(), actor(r))
	if err != nil {
		h.conversationError(w, err)
		return
	}
	write(w, 200, result)
}

func (h *Handler) newConversation(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := read(w, r, &input); err != nil {
		h.fail(w, err)
		return
	}
	if h.conversation == nil {
		writeError(w, 503, "temporarily_unavailable")
		return
	}
	result, err := h.conversation.Reset(r.Context(), actor(r), input.ConversationID)
	if err != nil {
		h.conversationError(w, err)
		return
	}
	write(w, 200, result)
}

func (h *Handler) sendMessage(w http.ResponseWriter, r *http.Request) {
	var input domain.Input
	if err := readLimit(w, r, &input, 65536); err != nil {
		h.fail(w, err)
		return
	}
	if h.conversation == nil {
		writeError(w, 503, "temporarily_unavailable")
		return
	}
	result, err := h.conversation.Send(r.Context(), actor(r), r.PathValue("id"), r.Header.Get("Idempotency-Key"), input)
	if err != nil {
		h.conversationError(w, err)
		return
	}
	code := 200
	if result.State == "pending" {
		code = 202
	}
	write(w, code, result)
}

func (h *Handler) conversationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalid):
		writeError(w, 400, "invalid_input")
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, 404, "not_found")
	case errors.Is(err, domain.ErrConflict):
		writeError(w, 409, "conflict")
	case errors.Is(err, domain.ErrFull):
		writeError(w, 409, "conversation_full")
	default:
		h.fail(w, err)
	}
}
