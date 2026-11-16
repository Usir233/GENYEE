package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	apprepo "github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/agent/tools"
	"github.com/Usir233/GENYEE/internal/mcp"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpSvcRepoAudit_RegisterMCPToolsFailsClosedOnUnrecognizedOrUnnormalizedMode
// verifies UP-SVC-AGENT-MCP-001: registerMCPTools must normalize whitespace/case
// on MCPSelectionMode and fail closed for unrecognized modes rather than falling
// into an open "all" branch that enumerates every workspace MCP service.
func TestUpSvcRepoAudit_RegisterMCPToolsFailsClosedOnUnrecognizedOrUnnormalizedMode(t *testing.T) {
	ctx := types.WithExecutionTenant(context.Background(), 7)

	for _, mode := range []string{"NONE", " none ", "disabled", "off", "unexpected-mode"} {
		t.Run("fail_closed_"+strings.TrimSpace(mode), func(t *testing.T) {
			rec := &recordingMCPService{}
			svc := &agentService{
				mcpServiceService: rec,
				mcpManager:        &mcp.MCPManager{},
			}
			cfg := &types.AgentConfig{
				MCPSelectionMode: mode,
				MCPServices:      []string{"mcp-1"},
			}
			svc.registerMCPTools(ctx, tools.NewToolRegistry(), cfg)
			assert.Equal(t, 0, rec.listAllCalls, "mode %q must never enumerate all tenant MCP services", mode)
			assert.Empty(t, rec.listByIDs, "mode %q must fail closed without loading selected MCP services", mode)
		})
	}

	t.Run("normalized_selected_mode", func(t *testing.T) {
		rec := &recordingMCPService{}
		svc := &agentService{
			mcpServiceService: rec,
			mcpManager:        &mcp.MCPManager{},
		}
		cfg := &types.AgentConfig{
			MCPSelectionMode: " Selected ",
			MCPServices:      []string{"mcp-allow-1"},
		}
		svc.registerMCPTools(ctx, tools.NewToolRegistry(), cfg)
		assert.Equal(t, 0, rec.listAllCalls)
		assert.Equal(t, []string{"mcp-allow-1"}, rec.listByIDs)
	})

	t.Run("normalized_all_mode", func(t *testing.T) {
		rec := &recordingMCPService{}
		svc := &agentService{
			mcpServiceService: rec,
			mcpManager:        &mcp.MCPManager{},
		}
		cfg := &types.AgentConfig{
			MCPSelectionMode: " ALL ",
		}
		svc.registerMCPTools(ctx, tools.NewToolRegistry(), cfg)
		assert.Equal(t, 1, rec.listAllCalls)
		assert.Empty(t, rec.listByIDs)
	})
}

type auditSyncTenantRepo struct {
	interfaces.TenantRepository
	tenant *types.Tenant
	err    error
}

func (r auditSyncTenantRepo) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return r.tenant, r.err
}

// TestUpSvcRepoAudit_ProcessSyncWorkspaceAndKBUnavailablePreserveTerminalAndRetryCancelWrite
// verifies UP-SVC-DATASOURCE-001:
// 1. When a workspace is suspended/deleted and writing the canceled SyncLog fails,
//    ProcessSync must return the write error for Asynq retry instead of acknowledging
//    a SyncLog stuck in "running".
// 2. When a finished round ("success" or "partial") is redelivered because LastSyncAt
//    was not yet persisted, an inactive/deleted workspace or deleted KB must preserve
//    the terminal SyncLog status rather than overwriting it to "canceled".
func TestUpSvcRepoAudit_ProcessSyncWorkspaceAndKBUnavailablePreserveTerminalAndRetryCancelWrite(t *testing.T) {
	writeErr := errors.New("sync_logs table temporarily unavailable")

	t.Run("suspended workspace retries when canceled state write fails", func(t *testing.T) {
		logRepo := &cancelAckLogRepo{
			processSyncSyncLogRepo: &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{
				"log-ws-1": {
					ID: "log-ws-1", DataSourceID: "ds-1", TenantID: 7,
					Status: types.SyncLogStatusRunning,
				},
			}},
			err: writeErr,
		}
		svc := &DataSourceService{
			dsRepo: cancelAckDSRepo{ds: &types.DataSource{
				ID: "ds-1", TenantID: 7, KnowledgeBaseID: "kb-1",
			}},
			syncLogRepo: logRepo,
			tenantRepo:  auditSyncTenantRepo{tenant: &types.Tenant{ID: 7, Status: "suspended"}},
		}
		payload, err := json.Marshal(types.DataSourceSyncPayload{
			DataSourceID: "ds-1", TenantID: 7, SyncLogID: "log-ws-1",
		})
		require.NoError(t, err)
		task := asynq.NewTask(types.TypeDataSourceSync, payload)

		require.ErrorIs(t, svc.ProcessSync(context.Background(), task), writeErr)

		logRepo.err = nil
		require.NoError(t, svc.ProcessSync(context.Background(), task))
		stored := logRepo.logs["log-ws-1"]
		assert.Equal(t, types.SyncLogStatusCanceled, stored.Status)
		assert.Equal(t, "workspace is inactive or deleted", stored.ErrorMessage)
		assert.NotNil(t, stored.FinishedAt)
	})

	for _, status := range []string{types.SyncLogStatusSuccess, types.SyncLogStatusPartial} {
		t.Run("deleted_workspace_preserves_"+status, func(t *testing.T) {
			startedAt := time.Now().UTC().Add(-2 * time.Minute)
			finishedAt := startedAt.Add(30 * time.Second)
			syncLog := &types.SyncLog{
				ID: "log-term-ws", DataSourceID: "ds-1", TenantID: 7,
				Status: status, StartedAt: startedAt, FinishedAt: &finishedAt,
			}
			syncLogRepo := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}
			svc := &DataSourceService{
				dsRepo: cancelAckDSRepo{ds: &types.DataSource{
					ID: "ds-1", TenantID: 7, KnowledgeBaseID: "kb-1",
				}},
				syncLogRepo: syncLogRepo,
				tenantRepo:  auditSyncTenantRepo{err: fmt.Errorf("lookup: %w", apprepo.ErrTenantNotFound)},
			}
			payload, err := json.Marshal(types.DataSourceSyncPayload{
				DataSourceID: "ds-1", TenantID: 7, SyncLogID: syncLog.ID,
			})
			require.NoError(t, err)

			require.NoError(t, svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload)))
			assert.Equal(t, status, syncLogRepo.logs[syncLog.ID].Status, "finished SyncLog must not be overwritten to canceled")
		})

		t.Run("deleted_kb_preserves_"+status, func(t *testing.T) {
			startedAt := time.Now().UTC().Add(-2 * time.Minute)
			finishedAt := startedAt.Add(30 * time.Second)
			syncLog := &types.SyncLog{
				ID: "log-term-kb", DataSourceID: "ds-1", TenantID: 7,
				Status: status, StartedAt: startedAt, FinishedAt: &finishedAt,
			}
			syncLogRepo := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}
			svc := &DataSourceService{
				dsRepo: cancelAckDSRepo{ds: &types.DataSource{
					ID: "ds-1", TenantID: 7, KnowledgeBaseID: "kb-deleted",
				}},
				syncLogRepo: syncLogRepo,
				kbService:   cancelAckKBService{err: fmt.Errorf("lookup: %w", apprepo.ErrKnowledgeBaseNotFound)},
			}
			payload, err := json.Marshal(types.DataSourceSyncPayload{
				DataSourceID: "ds-1", TenantID: 7, SyncLogID: syncLog.ID,
			})
			require.NoError(t, err)

			require.NoError(t, svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload)))
			assert.Equal(t, status, syncLogRepo.logs[syncLog.ID].Status, "finished SyncLog must not be overwritten to canceled")
		})
	}
}

// TestUpSvcRepoAudit_WikiIngestRejectsForeignTenantAndMovedKnowledge
// verifies UP-SVC-WIKI-ISOLATION-001: wikiIngestService.isKnowledgeGone,
// mapOneDocument, and reduceSlugUpdates must treat documents moved to another KB
// or belonging to another tenant as gone so source content and titles never leak
// across knowledge bases or workspaces.
func TestUpSvcRepoAudit_WikiIngestRejectsForeignTenantAndMovedKnowledge(t *testing.T) {
	cases := []struct {
		name      string
		makeDoc   func(page *types.WikiPage) *types.Knowledge
	}{
		{
			name: "moved to another knowledge base",
			makeDoc: func(page *types.WikiPage) *types.Knowledge {
				return &types.Knowledge{
					ID:              "kn-moved",
					TenantID:        page.TenantID,
					KnowledgeBaseID: "kb-private-other",
					Title:           "Confidential Moved Document",
					ParseStatus:     types.ParseStatusCompleted,
				}
			},
		},
		{
			name: "belongs to foreign tenant",
			makeDoc: func(page *types.WikiPage) *types.Knowledge {
				return &types.Knowledge{
					ID:              "kn-foreign",
					TenantID:        page.TenantID + 92,
					KnowledgeBaseID: page.KnowledgeBaseID,
					Title:           "Foreign Workspace Secret",
					ParseStatus:     types.ParseStatusCompleted,
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pageSvc, page := newWikiWriteGuardStore(t, "# Alpha\n\nExisting content.")
			doc := tc.makeDoc(page)
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, page.TenantID)

			svc := &wikiIngestService{
				wikiService: pageSvc,
				chunkRepo:   &wikiReparseChunks{},
				knowledgeSvc: &wikiKnowledgeLookupStub{lookup: func(_ context.Context, id string) (*types.Knowledge, error) {
					require.Equal(t, doc.ID, id)
					return doc, nil
				}},
			}

			gone, err := svc.isKnowledgeGone(ctx, page.KnowledgeBaseID, doc.ID)
			require.NoError(t, err)
			assert.True(t, gone, "knowledge outside (tenant=%d, kb=%s) must be treated as gone", page.TenantID, page.KnowledgeBaseID)

			batchCtx := &WikiBatchContext{
				SummaryContentByKnowledgeID: func(context.Context, string) string { return "" },
				SlugTitleMany:               func(context.Context, []string) map[string]string { return nil },
			}
			result, updates, err := svc.mapOneDocument(
				context.Background(),
				&wikiReparseModel{},
				WikiIngestPayload{TenantID: page.TenantID, KnowledgeBaseID: page.KnowledgeBaseID},
				WikiPendingOp{KnowledgeID: doc.ID, Op: WikiOpIngest},
				batchCtx,
			)
			require.NoError(t, err)
			assert.Nil(t, result, "mapOneDocument must skip foreign/moved knowledge")
			assert.Empty(t, updates)

			model := &templateCaptureChatModel{response: "SUMMARY: Leaked\n# Alpha\n\nLeaked content."}
			changed, _, _, _, err := svc.reduceSlugUpdates(
				context.Background(), model, page.KnowledgeBaseID, page.Slug,
				[]SlugUpdate{{
					Slug:        page.Slug,
					Type:        types.WikiPageTypeEntity,
					KnowledgeID: doc.ID,
					SourceRef:   doc.ID,
					Item:        extractedItem{Name: "Alpha", Details: "Leaked details"},
				}},
				page.TenantID, batchCtx, nil,
			)
			require.NoError(t, err)
			assert.False(t, changed, "reduceSlugUpdates must drop updates whose source moved or belongs to another tenant")
			assert.Empty(t, model.messages, "editor LLM must not be invoked for dropped foreign/moved updates")

			stored, err := pageSvc.GetPageBySlug(ctx, page.KnowledgeBaseID, page.Slug)
			require.NoError(t, err)
			assert.Equal(t, page.Content, stored.Content)
		})
	}
}
