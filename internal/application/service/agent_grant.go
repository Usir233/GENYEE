package service

import (
	"context"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
)

type agentGrantService struct {
	repo interfaces.AgentGrantRepository
}

// NewAgentGrantService wraps AgentGrantRepository behind the application service boundary.
func NewAgentGrantService(repo interfaces.AgentGrantRepository) interfaces.AgentGrantService {
	if repo == nil {
		return nil
	}
	return &agentGrantService{repo: repo}
}

func (s *agentGrantService) ListGrants(
	ctx context.Context, tenantID uint64, userID, scope, scopeID string,
) ([]*types.AgentGrant, error) {
	return s.repo.ListGrants(ctx, tenantID, userID, scope, scopeID)
}

func (s *agentGrantService) RevokeGrant(
	ctx context.Context, tenantID uint64, userID, id string, at time.Time,
) (bool, error) {
	return s.repo.RevokeGrant(ctx, tenantID, userID, id, at)
}

type agentPromptService struct {
	repo interfaces.AgentPromptRepository
}

// NewAgentPromptService wraps AgentPromptRepository behind the application service boundary.
func NewAgentPromptService(repo interfaces.AgentPromptRepository) interfaces.AgentPromptService {
	if repo == nil {
		return nil
	}
	return &agentPromptService{repo: repo}
}

func (s *agentPromptService) ListBySession(
	ctx context.Context, sessionID string, statuses []string,
) ([]*types.AgentPrompt, error) {
	return s.repo.ListBySession(ctx, sessionID, statuses)
}

func (s *agentPromptService) ListByMessages(
	ctx context.Context, sessionID string, messageIDs []string, statuses []string,
) ([]*types.AgentPrompt, error) {
	if byPage, ok := s.repo.(interface {
		ListByMessages(context.Context, string, []string, []string) ([]*types.AgentPrompt, error)
	}); ok {
		return byPage.ListByMessages(ctx, sessionID, messageIDs, statuses)
	}
	return s.repo.ListBySession(ctx, sessionID, statuses)
}
