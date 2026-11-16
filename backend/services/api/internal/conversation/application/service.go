package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"ai-bud/services/api/internal/conversation/domain"
	"ai-bud/services/api/internal/identity"
)

type Repository interface {
	Current(context.Context, identity.Actor) (domain.Conversation, error)
	Reset(context.Context, identity.Actor, string) (domain.Conversation, error)
	Begin(context.Context, identity.Actor, string, string, domain.Input) (domain.Turn, []domain.Turn, bool, error)
	Finish(context.Context, string, *domain.Answer, string) (domain.Turn, error)
}

type Responder interface {
	Generate(context.Context, identity.Actor, domain.Input) (domain.Answer, error)
}

type Service struct {
	repository Repository
	responder  Responder
}

func New(repository Repository, responder Responder) *Service {
	return &Service{repository: repository, responder: responder}
}

func (s *Service) Current(ctx context.Context, actor identity.Actor) (domain.Conversation, error) {
	return s.repository.Current(ctx, actor)
}

func (s *Service) Reset(ctx context.Context, actor identity.Actor, id string) (domain.Conversation, error) {
	if id == "" || len(id) > 128 {
		return domain.Conversation{}, domain.ErrInvalid
	}
	return s.repository.Reset(ctx, actor, id)
}

func (s *Service) Send(ctx context.Context, actor identity.Actor, id, key string, input domain.Input) (domain.Turn, error) {
	if id == "" || len(id) > 128 || key == "" || len(key) > 128 || strings.TrimSpace(key) != key {
		return domain.Turn{}, domain.ErrInvalid
	}
	if err := input.Validate(); err != nil {
		return domain.Turn{}, err
	}
	input.Question = strings.TrimSpace(input.Question)
	input.History = nil
	if input.Mode == "" {
		input.Mode = "offline"
	}
	if input.TopK == nil {
		k := uint32(3)
		input.TopK = &k
	}
	turn, previous, fresh, err := s.repository.Begin(ctx, actor, id, key, input)
	if err != nil || !fresh {
		return turn, err
	}
	input.History = history(previous)
	generation, cancel := context.WithTimeout(context.WithoutCancel(ctx), 61*time.Second)
	var answer domain.Answer
	if s.responder == nil {
		err = domain.ErrUnavailable
	} else {
		answer, err = s.responder.Generate(generation, actor, input)
	}
	cancel()
	save, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	if err != nil {
		return s.repository.Finish(save, turn.ID, nil, failureCode(err))
	}
	return s.repository.Finish(save, turn.ID, &answer, "")
}

func failureCode(err error) string {
	var dependency domain.DependencyError
	if errors.As(err, &dependency) {
		return dependency.Code
	}
	switch {
	case errors.Is(err, domain.ErrDeadline), errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, domain.ErrLimited):
		return "resource_exhausted"
	default:
		return "temporarily_unavailable"
	}
}

func history(turns []domain.Turn) []domain.Message {
	result := []domain.Message{}
	budget := 8000
	for i := len(turns) - 1; i >= 0 && len(result) < 6; i-- {
		turn := turns[i]
		if turn.State != "completed" || turn.Answer == nil {
			continue
		}
		texts := []string{}
		for _, step := range turn.Answer.Steps {
			texts = append(texts, step.Text)
		}
		texts = append(texts, turn.Answer.NextStep)
		question := truncate(turn.Question, 2000)
		answer := truncate(strings.TrimSpace(strings.Join(texts, "\n")), 2000)
		size := len([]rune(question)) + len([]rune(answer))
		if size > budget {
			break
		}
		if answer == "" {
			continue
		}
		budget -= size
		result = append([]domain.Message{{Role: "user", Content: question}, {Role: "assistant", Content: answer}}, result...)
	}
	return result
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return text
}
