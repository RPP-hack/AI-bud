package demo

import (
	"context"
	"errors"

	"ai-bud/services/api/internal/identity"
)

var ErrUnknownActor = errors.New("unknown actor")

type Directory struct{}

func (Directory) Resolve(ctx context.Context, id string) (identity.Actor, error) {
	if err := ctx.Err(); err != nil {
		return identity.Actor{}, err
	}
	switch id {
	case "alex":
		return identity.Actor{ID: "alex", Team: "notifications"}, nil
	case "sam":
		return identity.Actor{ID: "sam", Team: "payments"}, nil
	default:
		return identity.Actor{}, ErrUnknownActor
	}
}
