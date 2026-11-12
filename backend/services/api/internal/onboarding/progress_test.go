package onboarding

import (
	"ai-bud/services/api/internal/access/domain"
	"testing"
)

func TestAccessProgress(t *testing.T) {
	for _, tc := range []struct {
		s      domain.State
		ticket domain.TicketStatus
		want   string
	}{{domain.Proposed, "", "todo"}, {domain.Submitting, "", "waiting"}, {domain.Unknown, "", "waiting"}, {domain.Cancelled, "", "todo"}, {domain.Submitted, domain.Pending, "waiting"}, {domain.Submitted, domain.Granted, "done"}, {domain.Submitted, domain.Rejected, "blocked"}} {
		if p := ForAccess(domain.Request{State: tc.s, Ticket: domain.Ticket{Status: tc.ticket}}); p.State != tc.want || p.NextStep == "" {
			t.Fatal(tc, p)
		}
	}
}
