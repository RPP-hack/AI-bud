package domain

import (
	"errors"
	"testing"
	"time"
)

func valid() Request {
	r, _ := New("r", "u", "k", Parameters{"repo", "read", "first task"}, time.Now())
	return r
}
func TestValidation(t *testing.T) {
	for _, p := range []Parameters{{}, {"repo", "admin", "why"}, {"repo", "read", " "}, {"", "write", "why"}} {
		if !errors.Is(p.Validate(), ErrInvalid) {
			t.Fatalf("accepted %#v", p)
		}
	}
	for _, args := range [][3]string{{"", "u", "k"}, {"r", "", "k"}, {"r", "u", " "}} {
		if _, err := New(args[0], args[1], args[2], valid().Parameters, time.Now()); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := New("r", "u", "k", Parameters{}, time.Now()); err == nil {
		t.Fatal("invalid params")
	}
	for _, ticket := range []Ticket{{}, {"id", "invented"}} {
		if ticket.Validate() == nil {
			t.Fatal("invalid ticket")
		}
	}
}
func TestTransitions(t *testing.T) {
	r := valid()
	if r.Confirm(2) != ErrConflict {
		t.Fatal("stale version")
	}
	if err := r.Confirm(1); err != nil {
		t.Fatal(err)
	}
	if r.Cancel(1) != ErrConflict {
		t.Fatal("cancel after submission")
	}
	r.MarkUnknown()
	if r.State != Unknown {
		t.Fatal(r)
	}
	if r.Apply(Ticket{}) != ErrInvalid {
		t.Fatal("invalid result")
	}
	if err := r.Apply(Ticket{"t", Pending}); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(Ticket{"other", Pending}); err != ErrConflict {
		t.Fatal(err)
	}
	if err := r.Apply(Ticket{"t", Granted}); err != nil {
		t.Fatal(err)
	}
	for _, status := range []TicketStatus{Pending, Granted} {
		if err := r.Apply(Ticket{"t", status}); err != nil || r.Ticket.Status != Granted {
			t.Fatal(r, err)
		}
	}
	if r.Apply(Ticket{"t", Rejected}) != ErrConflict {
		t.Fatal("terminal regression")
	}
	r.MarkUnknown()
	if r.State != Submitted {
		t.Fatal(r)
	}
	r = valid()
	if r.Cancel(2) != ErrConflict {
		t.Fatal("stale cancel")
	}
	if r.Cancel(1) != nil {
		t.Fatal("cancel")
	}
	if r.Confirm(1) != ErrConflict || r.Apply(Ticket{"t", Pending}) != ErrConflict {
		t.Fatal("cancelled must be terminal")
	}
}
