package onboarding

import "ai-bud/services/api/internal/access/domain"

type AccessProgress struct{ State, NextStep string }

func ForAccess(r domain.Request) AccessProgress {
	switch {
	case r.Ticket.Status == domain.Granted:
		return AccessProgress{"done", "start_local_setup"}
	case r.Ticket.Status == domain.Rejected:
		return AccessProgress{"blocked", "contact_mentor"}
	case r.State == domain.Submitted:
		return AccessProgress{"waiting", "wait_for_approval"}
	case r.State == domain.Submitting || r.State == domain.Unknown:
		return AccessProgress{"waiting", "refresh_request"}
	case r.State == domain.Cancelled:
		return AccessProgress{"todo", "create_new_proposal"}
	default:
		return AccessProgress{"todo", "confirm_proposal"}
	}
}
