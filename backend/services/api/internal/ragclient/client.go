// Package ragclient adapts the internal Python gRPC service to Go's public API.
package ragclient

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"ai-bud/services/api/internal/identity"
	pb "ai-bud/services/api/internal/ragclient/ragv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const Timeout = 30 * time.Second

type Input struct {
	Question string  `json:"question"`
	Mode     string  `json:"mode"`
	TopK     *uint32 `json:"top_k"`
}

func (in Input) Validate() error {
	if strings.TrimSpace(in.Question) == "" || !utf8.ValidString(in.Question) || utf8.RuneCountInString(in.Question) > 4000 || strings.ContainsRune(in.Question, 0) ||
		(in.Mode != "" && in.Mode != "offline" && in.Mode != "llm") || (in.TopK != nil && (*in.TopK < 1 || *in.TopK > 50)) {
		return status.Error(codes.InvalidArgument, "invalid_input")
	}
	return nil
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

type Gateway interface {
	Answer(context.Context, identity.Actor, Input) (Answer, error)
	Search(context.Context, identity.Actor, Input) (SearchResult, error)
}

type Client struct {
	conn  *grpc.ClientConn
	stub  pb.RagServiceClient
	token string
}

func New(target, token string) (*Client, error) {
	if target == "" || token == "" {
		return nil, errors.New("RAG target and service token are required")
	}
	// This binary requires DEMO_MODE=true; plaintext is limited to local development.
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(4<<20), grpc.MaxCallSendMsgSize(64<<10)))
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, stub: pb.NewRagServiceClient(conn), token: token}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.token), cancel
}

func actorMessage(a identity.Actor) *pb.Actor {
	return &pb.Actor{UserId: a.ID, Role: a.Role, Team: a.Team}
}

func (c *Client) Answer(ctx context.Context, a identity.Actor, in Input) (Answer, error) {
	if err := in.Validate(); err != nil {
		return Answer{}, err
	}
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	mode := pb.AnswerMode_ANSWER_MODE_OFFLINE
	if in.Mode == "llm" {
		mode = pb.AnswerMode_ANSWER_MODE_LLM
	}
	r, err := c.stub.Answer(ctx, &pb.AnswerRequest{RequestId: rand.Text(), Question: in.Question, Actor: actorMessage(a), Mode: mode, TopK: in.TopK})
	if err != nil {
		return Answer{}, err
	}
	states := map[pb.AnswerStatus]string{
		pb.AnswerStatus_ANSWER_STATUS_ANSWERED: "answered", pb.AnswerStatus_ANSWER_STATUS_SOURCES_FOUND: "sources_found",
		pb.AnswerStatus_ANSWER_STATUS_INSUFFICIENT_EVIDENCE: "insufficient_evidence", pb.AnswerStatus_ANSWER_STATUS_CONFLICT: "conflict",
		pb.AnswerStatus_ANSWER_STATUS_NEEDS_REVIEW: "needs_review",
	}
	state, ok := states[r.Status]
	if !ok {
		return Answer{}, status.Error(codes.Internal, "invalid_rag_response")
	}
	out := Answer{RequestID: r.RequestId, Status: state, Mode: r.Mode, Steps: []Step{}, Sources: map[string]Source{}, NextStep: r.NextStep, Model: r.Model, Action: r.Action}
	for key, source := range r.Sources {
		out.Sources[key] = sourceView(source)
	}
	for _, step := range r.Steps {
		out.Steps = append(out.Steps, Step{Text: step.Text, SourceIDs: append([]string{}, step.SourceIds...)})
	}
	return out, nil
}

func (c *Client) Search(ctx context.Context, a identity.Actor, in Input) (SearchResult, error) {
	if err := in.Validate(); err != nil {
		return SearchResult{}, err
	}
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	r, err := c.stub.Search(ctx, &pb.SearchRequest{RequestId: rand.Text(), Query: in.Question, Actor: actorMessage(a), TopK: in.TopK})
	if err != nil {
		return SearchResult{}, err
	}
	out := SearchResult{RequestID: r.RequestId, Sources: []Source{}}
	for _, source := range r.Sources {
		out.Sources = append(out.Sources, sourceView(source))
	}
	return out, nil
}

func sourceView(s *pb.Source) Source {
	return Source{DocumentID: s.DocumentId, VersionID: s.VersionId, ChunkID: s.ChunkId, Title: s.Title, Text: s.Text,
		SourceURL: s.SourceUrl, PageNumber: s.PageNumber, Origin: s.Origin, Stale: s.Stale, ReviewBy: s.ReviewBy,
		StartChar: s.StartChar, EndChar: s.EndChar}
}
