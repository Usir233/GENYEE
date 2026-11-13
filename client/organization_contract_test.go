package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSharedResourcesPreserveResourceIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, field string
		load                    func(*Client) (any, error)
	}{
		{"knowledge base", "/api/v1/shared-knowledge-bases", `{"success":true,"data":[{"knowledge_base":{"id":"kb-1","name":"Shared KB"},"share_id":"share-1"}]}`, "knowledge_base", func(c *Client) (any, error) { return c.ListSharedKnowledgeBases(context.Background()) }},
		{"agent", "/api/v1/shared-agents", `{"success":true,"data":[{"agent":{"id":"agent-1","name":"Shared agent"},"share_id":"share-1","web_search_ready":true,"disabled_by_me":true}]}`, "agent", func(c *Client) (any, error) { return c.ListSharedAgents(context.Background()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					t.Errorf("path = %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}))
			defer s.Close()
			got, err := tc.load(NewClient(s.URL))
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(got)
			var items []map[string]any
			_ = json.Unmarshal(raw, &items)
			if len(items) != 1 || items[0][tc.field] == nil {
				t.Fatalf("resource identity discarded: %s", raw)
			}
			if tc.field == "agent" && (items[0]["web_search_ready"] != true || items[0]["disabled_by_me"] != true) {
				t.Errorf("shared agent availability discarded: %s", raw)
			}
		})
	}
}

func TestLegacyOrganizationUserMethodsDoNotTargetWorkspaces(t *testing.T) {
	for _, tc := range []struct {
		name, replacement string
		call              func(*Client) error
	}{
		{"search", "SearchTenantsForInvite", func(c *Client) error { _, err := c.SearchUsersForInvite(context.Background(), "org", "42"); return err }},
		{"update", "UpdateTenantMemberRole", func(c *Client) error { return c.UpdateMemberRole(context.Background(), "org", "42", "admin") }},
		{"remove", "RemoveTenantMember", func(c *Client) error { return c.RemoveMember(context.Background(), "org", "42") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("legacy user method reached workspace endpoint: %s %s", r.Method, r.URL)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"success":true,"data":[]}`)
			}))
			defer s.Close()
			err := tc.call(NewClient(s.URL))
			if err == nil || !strings.Contains(err.Error(), tc.replacement) {
				t.Fatalf("expected actionable migration error, got %v", err)
			}
		})
	}
}

func TestOrganizationWorkspaceMemberWireContract(t *testing.T) {
	const tenantID uint64 = 18446744073709551615
	for _, tc := range []struct {
		name, method, path, query, body string
		call                            func(*Client) error
	}{
		{"search", http.MethodGet, "/api/v1/organizations/org/search-tenants", "q=18446744073709551615", "", func(c *Client) error {
			rows, err := c.SearchTenantsForInvite(context.Background(), "org", tenantID)
			if err == nil && (len(rows) != 1 || rows[0].TenantID != tenantID || rows[0].TenantName != "Workspace") {
				t.Errorf("candidate = %+v", rows)
			}
			return err
		}},
		{"invite", http.MethodPost, "/api/v1/organizations/org/invite", "", `{"tenant_id":18446744073709551615,"role":"viewer"}`, func(c *Client) error { return c.InviteTenantMember(context.Background(), "org", tenantID, "viewer") }},
		{"update", http.MethodPut, "/api/v1/organizations/org/members/18446744073709551615", "", `{"role":"editor"}`, func(c *Client) error {
			return c.UpdateTenantMemberRole(context.Background(), "org", tenantID, "editor")
		}},
		{"remove", http.MethodDelete, "/api/v1/organizations/org/members/18446744073709551615", "", "", func(c *Client) error { return c.RemoveTenantMember(context.Background(), "org", tenantID) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method || r.URL.Path != tc.path || r.URL.RawQuery != tc.query {
					t.Errorf("request = %s %s", r.Method, r.URL)
				}
				raw, _ := io.ReadAll(r.Body)
				if strings.TrimSpace(string(raw)) != tc.body {
					t.Errorf("body = %s", raw)
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.name == "search" {
					_, _ = io.WriteString(w, `{"success":true,"data":[{"tenant_id":18446744073709551615,"tenant_name":"Workspace"}]}`)
				} else {
					_, _ = io.WriteString(w, `{"success":true}`)
				}
			}))
			defer s.Close()
			if err := tc.call(NewClient(s.URL)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
