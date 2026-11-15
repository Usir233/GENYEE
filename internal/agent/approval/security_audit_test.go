package approval

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/internal/agent/hitl"
	"github.com/Usir233/GENYEE/internal/agent/policy"
	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/event"
	"github.com/Usir233/GENYEE/internal/types"
)

type basicCheckerOnly struct{}

func (basicCheckerOnly) IsRequired(context.Context, uint64, string, string) (bool, error) {
	return true, nil
}

func (basicCheckerOnly) IsEnabled(context.Context, uint64, string, string) (bool, error) {
	return true, nil
}

type policyOnlyRiskChecker struct {
	row *types.MCPToolApproval
}

func (c policyOnlyRiskChecker) IsRequired(context.Context, uint64, string, string) (bool, error) {
	return c.row != nil && c.row.RequireApproval, nil
}

func (c policyOnlyRiskChecker) IsEnabled(context.Context, uint64, string, string) (bool, error) {
	return c.row == nil || c.row.Enabled, nil
}

func (c policyOnlyRiskChecker) Policy(context.Context, uint64, string, string) (*types.MCPToolApproval, error) {
	return c.row, nil
}

func (c policyOnlyRiskChecker) ObserveRisk(context.Context, uint64, string, string, string) error {
	return nil
}

func TestResolveResponseValidatesScopeAccessAndTrailingJSON(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 5}}, nil, nil)

	errs := make(chan error, 5)
	bus.On(event.EventToolApprovalRequired, func(_ context.Context, evt event.Event) error {
		id := evt.Data.(event.ToolApprovalRequiredData).PendingID
		go func() {
			for _, bad := range []string{
				`{"approved":true,"scope":"forever"}`,
				`{"approved":true,"access":"admin"}`,
				`{"approved":false,"scope":"session"}`,
				`{"approved":false,"access":"read"}`,
				`{"approved":true}{"approved":false}`,
			} {
				_, err := g.ResolveResponse(context.Background(), hitl.ResolveRequest{
					TenantID: 1, UserID: "u1", Kind: types.AgentPromptKindToolApproval,
					PromptID: id, Response: json.RawMessage(bad),
				})
				errs <- err
			}
			_, _ = g.ResolveResponse(context.Background(), hitl.ResolveRequest{
				TenantID: 1, UserID: "u1", Kind: types.AgentPromptKindToolApproval,
				PromptID: id, Response: json.RawMessage(`{"approved":false,"reason":"stop"}`),
			})
		}()
		return nil
	})

	d, err := g.RequestHostAndWait(context.Background(), hostRequest(bus))
	require.NoError(t, err)
	require.False(t, d.Approved)
	for i := 0; i < 5; i++ {
		require.ErrorIs(t, <-errs, ErrInvalidDecision)
	}

	// MCP OAuth response validator must also enforce strict single-object schema with required "authorized".
	for _, badOAuth := range []string{
		`{}`,
		`{"authorized":true,"extra":1}`,
		`{"authorized":true}{"authorized":false}`,
	} {
		require.Error(t, validateMCPOAuthResponse(nil, json.RawMessage(badOAuth)), "oauth response %s", badOAuth)
	}
	require.NoError(t, validateMCPOAuthResponse(nil, json.RawMessage(`{"authorized":true,"reason":"ok"}`)))
}

func TestRequestHostAndWaitCannotWidenProposedGrantOrSessionScope(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 5}}, nil, nil)
	req := hostRequest(bus)
	req.Host = event.HostApprovalPayload{
		Reason:       "sandbox_denied",
		Command:      "cat /outside/secret.txt",
		Cwd:          "/w",
		AllowSession: false,
		GrantPath:    "/outside/secret.txt",
		GrantAccess:  "read",
	}

	var resolved event.ToolApprovalResolvedData
	bus.On(event.EventToolApprovalRequired, func(_ context.Context, evt event.Event) error {
		id := evt.Data.(event.ToolApprovalRequiredData).PendingID
		go func() {
			_, _ = g.ResolveResponse(context.Background(), hitl.ResolveRequest{
				TenantID: 1, UserID: "u1", Kind: types.AgentPromptKindToolApproval,
				PromptID: id, Response: json.RawMessage(`{"approved":true,"scope":"session","access":"write"}`),
			})
		}()
		return nil
	})
	bus.On(event.EventToolApprovalResolved, func(_ context.Context, evt event.Event) error {
		resolved = evt.Data.(event.ToolApprovalResolvedData)
		return nil
	})

	d, err := g.RequestHostAndWait(context.Background(), req)
	require.NoError(t, err)
	require.True(t, d.Approved)
	require.Empty(t, d.Scope, "AllowSession=false must not yield a session scope")
	require.Equal(t, "read", d.Access, "read grant proposal must never widen to write")
	require.Empty(t, resolved.Scope)
}

func TestGateActionAndToolRisksFailClosedOnMissingIdentityOrCheckerType(t *testing.T) {
	ctx := context.Background()
	ro := true
	roTool := &types.MCPTool{
		Name:        "ro_tool",
		Annotations: &types.MCPToolAnnotations{ReadOnlyHint: &ro},
	}

	gRisk := NewGate(nil, policyOnlyRiskChecker{
		row: &types.MCPToolApproval{
			TenantID:        1,
			ServiceID:       "svc",
			ToolName:        "ro_tool",
			Enabled:         true,
			Risk:            string(policy.RiskDestructive),
			RequireApproval: true,
		},
	}, nil)

	// Missing tenant or service or tool identity must fail closed instead of trusting readOnlyHint.
	actZeroTenant := gRisk.Action(ctx, 0, "svc", roTool)
	require.Equal(t, policy.RiskWrite, actZeroTenant.Risk)
	require.True(t, actZeroTenant.Always)

	actEmptyService := gRisk.Action(ctx, 1, "", roTool)
	require.Equal(t, policy.RiskWrite, actEmptyService.Risk)
	require.True(t, actEmptyService.Always)

	_, err := gRisk.ToolRisks(ctx, 0, "svc", []*types.MCPTool{roTool})
	require.Error(t, err)

	_, err = gRisk.ToolRisks(ctx, 1, "", []*types.MCPTool{roTool})
	require.Error(t, err)

	// RiskChecker without ListByService must still enforce stored per-tool policy in ToolRisks.
	risks, err := gRisk.ToolRisks(ctx, 1, "svc", []*types.MCPTool{roTool})
	require.NoError(t, err)
	require.Equal(t, policy.RiskDestructive, risks["ro_tool"])

	// Checker that does not implement RiskChecker leaves every MCP tool at RiskWrite.
	gBasic := NewGate(nil, basicCheckerOnly{}, nil)
	basicRisks, err := gBasic.ToolRisks(ctx, 1, "svc", []*types.MCPTool{roTool})
	require.NoError(t, err)
	require.Equal(t, policy.RiskWrite, basicRisks["ro_tool"])
}

func TestSecureFormAndResumeEntryPointsFailClosedOnMissingOwnerOrKind(t *testing.T) {
	ctx := context.Background()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 2}}, nil, nil)

	noTenant := secureFormRequest(event.NewEventBus())
	noTenant.TenantID = 0
	_, err := g.RequestSecureForm(ctx, noTenant)
	require.Error(t, err)

	noUser := secureFormRequest(event.NewEventBus())
	noUser.UserID = "   "
	_, err = g.RequestSecureForm(ctx, noUser)
	require.Error(t, err)

	_, err = g.ResolveSecureForm(ctx, hitl.ResolveRequest{
		TenantID: 0, UserID: "web_user:u1", PromptID: "p1", Response: json.RawMessage(`{"declined":true}`),
	})
	require.ErrorIs(t, err, ErrTenantMismatch)

	_, err = g.ResolveSecureForm(ctx, hitl.ResolveRequest{
		TenantID: 1, UserID: "  ", PromptID: "p1", Response: json.RawMessage(`{"declined":true}`),
	})
	require.ErrorIs(t, err, ErrUserMismatch)

	var nilGate *Gate
	_, err = nilGate.SecureFormPrompt(ctx, "p1")
	require.Error(t, err)

	// Nil or mismatched-kind prompts passed to Resume* must fail closed without panicking or consuming.
	require.False(t, g.ResumeDecision(ctx, nil, nil).Approved)
	require.False(t, g.ResumeAnswer(ctx, nil, nil).Answered)
	require.False(t, g.ResumeSecureForm(ctx, nil, nil).Answered)

	wrongPrompt := &types.AgentPrompt{
		ID: "p-wrong", TenantID: 1, UserID: "u1", Kind: types.AgentPromptKindAskUser,
		Status: types.AgentPromptAnswered, OwnerInstance: "owner-1",
		Response: types.JSON(`{"approved":true,"handle":"cred_1","fields":["password"],"selected":["dev"]}`),
	}
	require.NoError(t, g.broker.Store().Create(ctx, wrongPrompt))

	require.False(t, g.ResumeDecision(ctx, wrongPrompt, nil).Approved)
	require.False(t, g.ResumeSecureForm(ctx, wrongPrompt, nil).Answered)
	stored, err := g.broker.Store().Get(ctx, "p-wrong")
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptAnswered, stored.Status, "wrong-kind Resume must not consume the prompt")
}
