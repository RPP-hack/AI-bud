package demo

import (
	"ai-bud/services/api/internal/access/domain"
	"ai-bud/services/api/internal/identity"
)

type Policy struct{}

func (Policy) CanRequest(a identity.Actor, p domain.Parameters) bool {
	return a.ID != "" && p.Access == "read" && ((a.Team == "notifications" && p.Resource == "notifications") || (a.Team == "payments" && p.Resource == "payments"))
}
