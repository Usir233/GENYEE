package agentcmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/cli/internal/cmdutil"
	"github.com/Usir233/GENYEE/cli/internal/iostreams"
	"github.com/Usir233/GENYEE/cli/internal/prompt"
	sdk "github.com/Usir233/GENYEE/client"
)

func TestW7AgentResourceIDAndNaNAndCopyPreservationAndRemoveKBName(t *testing.T) {
	t.Run("rejects path-traversing or shell-unsafe agent IDs across commands", func(t *testing.T) {
		_, _ = iostreams.SetForTest(t)
		f := &cmdutil.Factory{
			Client:   func() (*sdk.Client, error) { return sdk.NewClient("http://127.0.0.1:1"), nil },
			Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
		}
		for _, badID := range []string{"../tenants", "ag;rm -rf /", "a/b"} {
			st := NewCmdStatus(f)
			st.SetArgs([]string{badID, "--format", "json"})
			st.SilenceErrors, st.SilenceUsage = true, true
			err := st.Execute()
			require.Error(t, err, "agent status must reject invalid id %q", badID)

			ck := NewCmdCheck(f)
			ck.SetArgs([]string{badID, "--format", "json"})
			ck.SilenceErrors, ck.SilenceUsage = true, true
			require.Error(t, ck.Execute(), "agent check must reject invalid id %q", badID)

			vw := NewCmdView(f)
			vw.SetArgs([]string{badID, "--format", "json"})
			vw.SilenceErrors, vw.SilenceUsage = true, true
			require.Error(t, vw.Execute(), "agent view must reject invalid id %q", badID)

			del := withRootHarnessAgent(NewCmdDelete(f), badID, "--format", "json")
			del.SetArgs([]string{"agent", "delete", badID, "--format", "json"})
			require.Error(t, del.Execute(), "agent delete must reject invalid id %q before exit-10 retry_argv", badID)

			ed := withRootHarnessAgent(NewCmdEdit(f), badID, "--name", "NewName", "--format", "json")
			require.Error(t, ed.Execute(), "agent update must reject invalid id %q before exit-10 retry_argv", badID)
		}
	})

	t.Run("rejects NaN and Inf --temperature", func(t *testing.T) {
		for _, raw := range []string{"NaN", "+Inf", "-Inf"} {
			c := NewCmdCreate(nil)
			c.SetArgs([]string{"Bot", "--model", "m1", "--temperature", raw})
			c.SilenceErrors, c.SilenceUsage = true, true
			require.Error(t, c.Execute(), "agent create must reject --temperature %s", raw)

			e := NewCmdEdit(nil)
			e.SetArgs([]string{"ag_abc", "--temperature", raw})
			e.SilenceErrors, e.SilenceUsage = true, true
			require.Error(t, e.Execute(), "agent update must reject --temperature %s", raw)
		}
	})

	t.Run("agent create --from preserves copied Description when --description is omitted and rejects --config-file", func(t *testing.T) {
		_, _ = iostreams.SetForTest(t)
		svc := &fakeCreateSvc{
			copyResp: &sdk.Agent{
				ID:          "ag_copied",
				Name:        "Original",
				Description: "Keep this template description",
				Config:      &sdk.AgentConfig{ModelID: "m_old", SystemPrompt: "base prompt"},
			},
			updateResp: &sdk.Agent{ID: "ag_copied", Name: "Cloned"},
		}
		opts := &CreateOptions{
			Name:  "Cloned",
			Model: "m_new",
			From:  "ag_source",
		}
		require.NoError(t, runCreate(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
		require.NotNil(t, svc.updateReq)
		assert.Equal(t, "Keep this template description", svc.updateReq.Description)

		c := NewCmdCreate(nil)
		c.SetArgs([]string{"Cloned", "--model", "m1", "--from", "ag_source", "--config-file", "cfg.yaml"})
		c.SilenceErrors, c.SilenceUsage = true, true
		require.Error(t, c.Execute(), "--from and --config-file must be mutually exclusive")
	})

	t.Run("agent update --remove-kb resolves KB name to ID", func(t *testing.T) {
		_, _ = iostreams.SetForTest(t)
		kbUUID := "11111111-1111-1111-1111-111111111111"
		var updatedKBs []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/v1/knowledge-bases":
				_, _ = w.Write([]byte(`{"success":true,"data":[{"id":"` + kbUUID + `","name":"Engineering Docs"}]}`))
			case r.Method == http.MethodGet && r.URL.Path == "/api/v1/agents/ag_abc":
				_, _ = w.Write([]byte(`{"success":true,"data":{"id":"ag_abc","name":"Bot","config":{"model_id":"m1","knowledge_bases":["` + kbUUID + `"]}}}`))
			case r.Method == http.MethodPut && r.URL.Path == "/api/v1/agents/ag_abc":
				var body sdk.UpdateAgentRequest
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body.Config != nil {
					updatedKBs = body.Config.KnowledgeBases
				}
				_, _ = w.Write([]byte(`{"success":true,"data":{"id":"ag_abc","name":"Bot","config":{"model_id":"m1","knowledge_bases":[]}}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		f := &cmdutil.Factory{
			Client:   func() (*sdk.Client, error) { return sdk.NewClient(srv.URL), nil },
			Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
		}
		root := withRootHarnessAgent(NewCmdEdit(f), "ag_abc", "--remove-kb", "Engineering Docs", "-y", "--format", "json")
		require.NoError(t, root.Execute())
		assert.Empty(t, updatedKBs, "--remove-kb by name must resolve to KB UUID and detach it")
	})

	t.Run("dry-run does not fail on missing --config-file or --system-prompt-file", func(t *testing.T) {
		_, _ = iostreams.SetForTest(t)
		c := NewCmdCreate(nil)
		c.SetArgs([]string{"Bot", "--model", "m1", "--config-file", "/nonexistent/cfg.yaml", "--dry-run"})
		c.SilenceErrors, c.SilenceUsage = true, true
		require.NoError(t, c.Execute())

		f := &cmdutil.Factory{
			Client:   func() (*sdk.Client, error) { return nil, nil },
			Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
		}
		root := withRootHarnessAgent(NewCmdEdit(f), "ag_abc", "--system-prompt-file", "/nonexistent/p.md", "--dry-run", "--format", "json")
		require.NoError(t, root.Execute())
	})
}
