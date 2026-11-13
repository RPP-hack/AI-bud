package domain

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalid   = errors.New("invalid input")
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
	ErrConflict  = errors.New("conflict")
)

type State string

const (
	Proposed   State = "proposed"
	Submitting State = "submitting"
	Submitted  State = "submitted"
	Unknown    State = "unknown"
	Cancelled  State = "cancelled"
)

type TicketStatus string

const (
	Pending  TicketStatus = "pending"
	Granted  TicketStatus = "granted"
	Rejected TicketStatus = "rejected"
)

type Parameters struct{ Resource, Access, Reason string }

func (p Parameters) Validate() error {
	if strings.TrimSpace(p.Resource) == "" || len(p.Resource) > 128 ||
		(p.Access != "read" && p.Access != "write") || strings.TrimSpace(p.Reason) == "" || len(p.Reason) > 1000 {
		return ErrInvalid
	}
	return nil
}

type Ticket struct {
	ID     string
	Status TicketStatus
}

func (t Ticket) Validate() error {
	if t.ID == "" || (t.Status != Pending && t.Status != Granted && t.Status != Rejected) {
		return ErrInvalid
	}
	return nil
}

type Request struct {
	ID, OwnerID, IdempotencyKey string
	Parameters                  Parameters
	State                       State
	Ticket                      Ticket
	Version                     int64
	CreatedAt, UpdatedAt        time.Time
}

func (r Request) NeedsReconciliation() bool {
	return r.State == Submitting || r.State == Unknown || (r.State == Submitted && r.Ticket.Status == Pending)
}

func New(id, owner, key string, p Parameters, now time.Time) (Request, error) {
	if id == "" || owner == "" || strings.TrimSpace(key) == "" || len(key) > 128 {
		return Request{}, ErrInvalid
	}
	if err := p.Validate(); err != nil {
		return Request{}, err
	}
	return Request{ID: id, OwnerID: owner, IdempotencyKey: key, Parameters: p, State: Proposed, Version: 1, CreatedAt: now, UpdatedAt: now}, nil
}

func (r *Request) Confirm(version int64) error {
	if r.State != Proposed || r.Version != version {
		return ErrConflict
	}
	r.State = Submitting
	return nil
}

func (r *Request) Cancel(version int64) error {
	if r.State != Proposed || r.Version != version {
		return ErrConflict
	}
	r.State = Cancelled
	return nil
}

func (r *Request) MarkUnknown() {
	if r.State == Submitting {
		r.State = Unknown
	}
}

func (r *Request) Apply(t Ticket) error {
	if err := t.Validate(); err != nil {
		return err
	}
	if r.State != Submitting && r.State != Unknown && r.State != Submitted {
		return ErrConflict
	}
	if r.Ticket.ID != "" && r.Ticket.ID != t.ID {
		return ErrConflict
	}
	if r.Ticket.Status == Granted || r.Ticket.Status == Rejected {
		if t.Status == Pending || t.Status == r.Ticket.Status {
			return nil
		}
		return ErrConflict
	}
	r.Ticket = t
	r.State = Submitted
	return nil
}
