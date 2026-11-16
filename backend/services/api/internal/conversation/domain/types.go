package domain

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

type Input struct {
	Question string    `json:"question"`
	Mode     string    `json:"mode"`
	TopK     *uint32   `json:"top_k"`
	History  []Message `json:"-"`
}

type Source struct {
	DocumentID string  `json:"document_id"`
	VersionID  string  `json:"version_id"`
	ChunkID    string  `json:"chunk_id"`
	Title      string  `json:"title"`
	Text       string  `json:"text"`
	SourceURL  *string `json:"source_url"`
	PageNumber *uint32 `json:"page_number"`
	Origin     string  `json:"origin"`
	Stale      bool    `json:"stale"`
	ReviewBy   *string `json:"review_by"`
	StartChar  uint32  `json:"start_char"`
	EndChar    uint32  `json:"end_char"`
}

type Step struct {
	Text      string   `json:"text"`
	SourceIDs []string `json:"source_ids"`
}

type Answer struct {
	RequestID string            `json:"request_id"`
	Status    string            `json:"status"`
	Mode      string            `json:"mode"`
	Steps     []Step            `json:"steps"`
	Sources   map[string]Source `json:"sources"`
	NextStep  string            `json:"next_step"`
	Model     *string           `json:"model"`
	Action    *string           `json:"action"`
}

type SearchResult struct {
	RequestID string   `json:"request_id"`
	Sources   []Source `json:"sources"`
}

func (in Input) Validate() error {
	if strings.TrimSpace(in.Question) == "" || !utf8.ValidString(in.Question) || utf8.RuneCountInString(in.Question) > 4000 || invalidControl(in.Question) ||
		(in.Mode != "" && in.Mode != "offline" && in.Mode != "llm") || (in.TopK != nil && (*in.TopK < 1 || *in.TopK > 50)) {
		return ErrInvalid
	}
	return nil
}

var (
	ErrInvalid     = errors.New("invalid_input")
	ErrNotFound    = errors.New("not_found")
	ErrConflict    = errors.New("conflict")
	ErrUnavailable = errors.New("temporarily_unavailable")
	ErrDeadline    = errors.New("deadline_exceeded")
	ErrLimited     = errors.New("resource_exhausted")
	ErrFull        = errors.New("conversation_full")
)

func invalidControl(s string) bool {
	for _, r := range s {
		if r < 32 && r != '\n' && r != '\r' && r != '\t' || r == 127 {
			return true
		}
	}
	return false
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type Turn struct {
	ID        string    `json:"id"`
	ClientID  string    `json:"client_id"`
	Question  string    `json:"question"`
	Mode      string    `json:"mode"`
	TopK      uint32    `json:"top_k"`
	State     string    `json:"state"`
	Answer    *Answer   `json:"answer,omitempty"`
	ErrorCode string    `json:"error_code,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
type Conversation struct {
	ID    string `json:"id"`
	Turns []Turn `json:"turns"`
}
