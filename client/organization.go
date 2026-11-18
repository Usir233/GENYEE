package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Organization represents a collaboration organization
type Organization struct {
	ID                     string     `json:"id"`
	Name                   string     `json:"name"`
	Description            string     `json:"description"`
	Avatar                 string     `json:"avatar,omitempty"`
	OwnerID                string     `json:"owner_id"`
	InviteCode             string     `json:"invite_code,omitempty"`
	InviteCodeExpiresAt    *time.Time `json:"invite_code_expires_at,omitempty"`
	InviteCodeValidityDays int        `json:"invite_code_validity_days"`
	RequireApproval        bool       `json:"require_approval"`
	Searchable             bool       `json:"searchable"`
	MemberLimit            int        `json:"member_limit"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

// OrganizationResponse represents an organization in API responses (with counts)
type OrganizationResponse struct {
	ID                      string     `json:"id"`
	Name                    string     `json:"name"`
	Description             string     `json:"description"`
	Avatar                  string     `json:"avatar,omitempty"`
	OwnerID                 string     `json:"owner_id"`
	InviteCode              string     `json:"invite_code,omitempty"`
	InviteCodeExpiresAt     *time.Time `json:"invite_code_expires_at,omitempty"`
	InviteCodeValidityDays  int        `json:"invite_code_validity_days"`
	RequireApproval         bool       `json:"require_approval"`
	Searchable              bool       `json:"searchable"`
	MemberLimit             int        `json:"member_limit"`
	MemberCount             int        `json:"member_count"`
	ShareCount              int        `json:"share_count"`
	AgentShareCount         int        `json:"agent_share_count"`
	PendingJoinRequestCount int        `json:"pending_join_request_count"`
	IsOwner                 bool       `json:"is_owner"`
	MyRole                  string     `json:"my_role,omitempty"`
	HasPendingUpgrade       bool       `json:"has_pending_upgrade"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

// CreateOrganizationRequest represents a request to create an organization
type CreateOrganizationRequest struct {
	Name                   string `json:"name"`
	Description            string `json:"description,omitempty"`
	Avatar                 string `json:"avatar,omitempty"`
	InviteCodeValidityDays *int   `json:"invite_code_validity_days,omitempty"`
	MemberLimit            *int   `json:"member_limit,omitempty"`
}

// UpdateOrganizationRequest represents a request to update an organization
type UpdateOrganizationRequest struct {
	Name                   *string `json:"name,omitempty"`
	Description            *string `json:"description,omitempty"`
	Avatar                 *string `json:"avatar,omitempty"`
	RequireApproval        *bool   `json:"require_approval,omitempty"`
	Searchable             *bool   `json:"searchable,omitempty"`
	InviteCodeValidityDays *int    `json:"invite_code_validity_days,omitempty"`
	MemberLimit            *int    `json:"member_limit,omitempty"`
}

// OrganizationMemberResponse represents a member in API responses
type OrganizationMemberResponse struct {
	RepresentativeUserID string    `json:"representative_user_id"`
	TenantName           string    `json:"tenant_name,omitempty"`
	ID                   string    `json:"id"`
	UserID               string    `json:"user_id"`
	Username             string    `json:"username"`
	Email                string    `json:"email"`
	Avatar               string    `json:"avatar"`
	Role                 string    `json:"role"`
	TenantID             uint64    `json:"tenant_id"`
	JoinedAt             time.Time `json:"joined_at"`
}

// KnowledgeBaseShareResponse represents a KB share record in API responses
type KnowledgeBaseShareResponse struct {
	ID                string    `json:"id"`
	KnowledgeBaseID   string    `json:"knowledge_base_id"`
	KnowledgeBaseName string    `json:"knowledge_base_name"`
	OrganizationID    string    `json:"organization_id"`
	OrganizationName  string    `json:"organization_name"`
	SharedByUserID    string    `json:"shared_by_user_id"`
	SharedByUsername  string    `json:"shared_by_username"`
	SourceTenantID    uint64    `json:"source_tenant_id"`
	Permission        string    `json:"permission"`
	MyRoleInOrg       string    `json:"my_role_in_org"`
	MyPermission      string    `json:"my_permission"`
	CreatedAt         time.Time `json:"created_at"`
}

// AgentShareResponse represents an agent share record in API responses
type AgentShareResponse struct {
	ID               string    `json:"id"`
	AgentID          string    `json:"agent_id"`
	AgentName        string    `json:"agent_name"`
	OrganizationID   string    `json:"organization_id"`
	OrganizationName string    `json:"organization_name"`
	SharedByUserID   string    `json:"shared_by_user_id"`
	SharedByUsername string    `json:"shared_by_username"`
	SourceTenantID   uint64    `json:"source_tenant_id"`
	Permission       string    `json:"permission"`
	CreatedAt        time.Time `json:"created_at"`
}

// JoinRequestResponse represents a join request in API responses
type JoinRequestResponse struct {
	ID            string     `json:"id"`
	UserID        string     `json:"user_id"`
	Username      string     `json:"username"`
	Email         string     `json:"email"`
	Message       string     `json:"message"`
	RequestType   string     `json:"request_type"`
	PrevRole      string     `json:"prev_role"`
	RequestedRole string     `json:"requested_role"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	ReviewedAt    *time.Time `json:"reviewed_at,omitempty"`
}

// SharedKnowledgeBaseInfo represents a shared knowledge base
type SharedKnowledgeBaseInfo struct {
	KnowledgeBase  *KnowledgeBase `json:"knowledge_base"`
	ShareID        string         `json:"share_id"`
	OrganizationID string         `json:"organization_id"`
	OrgName        string         `json:"org_name"`
	Permission     string         `json:"permission"`
	SourceTenantID uint64         `json:"source_tenant_id"`
	SharedAt       time.Time      `json:"shared_at"`
}

// SharedAgentInfo represents a shared agent
type SharedAgentInfo struct {
	Agent            *Agent    `json:"agent"`
	SharedByUserID   string    `json:"shared_by_user_id,omitempty"`
	SharedByUsername string    `json:"shared_by_username,omitempty"`
	WebSearchReady   bool      `json:"web_search_ready"`
	DisabledByMe     bool      `json:"disabled_by_me"`
	ShareID          string    `json:"share_id"`
	OrganizationID   string    `json:"organization_id"`
	OrgName          string    `json:"org_name"`
	Permission       string    `json:"permission"`
	SourceTenantID   uint64    `json:"source_tenant_id"`
	SharedAt         time.Time `json:"shared_at"`
}

// UserInfo represents user information for API responses
type UserInfo struct {
	ID                  string    `json:"id"`
	Username            string    `json:"username"`
	Email               string    `json:"email"`
	Avatar              string    `json:"avatar"`
	TenantID            uint64    `json:"tenant_id"`
	IsActive            bool      `json:"is_active"`
	CanAccessAllTenants bool      `json:"can_access_all_tenants"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// --- Organization CRUD ---

// CreateOrganization creates a new organization
func (c *Client) CreateOrganization(
	ctx context.Context,
	req *CreateOrganizationRequest,
) (*OrganizationResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/organizations", req, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool                  `json:"success"`
		Data    *OrganizationResponse `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ListMyOrganizations lists organizations the current user belongs to
func (c *Client) ListMyOrganizations(ctx context.Context) ([]OrganizationResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/organizations", nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Organizations []OrganizationResponse `json:"organizations"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Organizations, nil
}

// GetOrganization gets an organization by ID
func (c *Client) GetOrganization(ctx context.Context, orgID string) (*OrganizationResponse, error) {
	path := fmt.Sprintf("/api/v1/organizations/%s", escapePathSegment(orgID))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool                  `json:"success"`
		Data    *OrganizationResponse `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// UpdateOrganization updates an organization
func (c *Client) UpdateOrganization(
	ctx context.Context,
	orgID string,
	req *UpdateOrganizationRequest,
) (*OrganizationResponse, error) {
	path := fmt.Sprintf("/api/v1/organizations/%s", escapePathSegment(orgID))
	resp, err := c.doRequest(ctx, http.MethodPut, path, req, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool                  `json:"success"`
		Data    *OrganizationResponse `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// DeleteOrganization deletes an organization
func (c *Client) DeleteOrganization(ctx context.Context, orgID string) error {
	path := fmt.Sprintf("/api/v1/organizations/%s", escapePathSegment(orgID))
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// --- Organization membership ---

// JoinOrganizationByInviteCode joins an organization using an invite code
func (c *Client) JoinOrganizationByInviteCode(ctx context.Context, inviteCode string) error {
	req := map[string]string{"invite_code": inviteCode}
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/organizations/join", req, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// SubmitJoinRequest submits a join request for organizations that require approval
func (c *Client) SubmitJoinRequest(ctx context.Context, inviteCode, message, role string) error {
	req := map[string]string{
		"invite_code": inviteCode,
		"message":     message,
		"role":        role,
	}
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/organizations/join-request", req, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// SearchOrganizations searches for discoverable organizations
func (c *Client) SearchOrganizations(
	ctx context.Context,
	keyword string,
	page, pageSize int,
) ([]OrganizationResponse, error) {
	q := url.Values{}
	if keyword != "" {
		q.Set("keyword", keyword)
	}
	if page > 0 {
		q.Set("page", fmt.Sprintf("%d", page))
	}
	if pageSize > 0 {
		q.Set("page_size", fmt.Sprintf("%d", pageSize))
	}
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/organizations/search", nil, q)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Organizations []OrganizationResponse `json:"organizations"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Organizations, nil
}

// JoinByOrganizationID joins a searchable organization by its ID
func (c *Client) JoinByOrganizationID(ctx context.Context, orgID, message, role string) error {
	req := map[string]string{
		"organization_id": orgID,
		"message":         message,
		"role":            role,
	}
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/organizations/join-by-id", req, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// PreviewOrganizationByInviteCode previews an organization before joining
func (c *Client) PreviewOrganizationByInviteCode(ctx context.Context, code string) (*OrganizationResponse, error) {
	path := fmt.Sprintf("/api/v1/organizations/preview/%s", escapePathSegment(code))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool                  `json:"success"`
		Data    *OrganizationResponse `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// LeaveOrganization leaves an organization
func (c *Client) LeaveOrganization(ctx context.Context, orgID string) error {
	path := fmt.Sprintf("/api/v1/organizations/%s/leave", escapePathSegment(orgID))
	resp, err := c.doRequest(ctx, http.MethodPost, path, nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// RequestRoleUpgrade requests a role upgrade in an organization
func (c *Client) RequestRoleUpgrade(ctx context.Context, orgID, requestedRole, message string) error {
	req := map[string]string{
		"requested_role": requestedRole,
		"message":        message,
	}
	path := fmt.Sprintf("/api/v1/organizations/%s/request-upgrade", escapePathSegment(orgID))
	resp, err := c.doRequest(ctx, http.MethodPost, path, req, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// GenerateInviteCode generates a new invite code for an organization
func (c *Client) GenerateInviteCode(ctx context.Context, orgID string) (string, error) {
	path := fmt.Sprintf("/api/v1/organizations/%s/invite-code", escapePathSegment(orgID))
	resp, err := c.doRequest(ctx, http.MethodPost, path, nil, nil)
	if err != nil {
		return "", err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			InviteCode string `json:"invite_code"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return "", err
	}
	return result.Data.InviteCode, nil
}

// TenantInviteCandidate identifies a workspace eligible for organization membership.
type TenantInviteCandidate struct {
	TenantID   uint64 `json:"tenant_id"`
	TenantName string `json:"tenant_name"`
}

// SearchTenantsForInvite resolves an exact workspace ID the caller administers.
// It does not search user identities or workspace names.
func (c *Client) SearchTenantsForInvite(
	ctx context.Context,
	orgID string,
	tenantID uint64,
) ([]TenantInviteCandidate, error) {
	if tenantID == 0 {
		return nil, fmt.Errorf("workspace ID must be nonzero")
	}
	q := url.Values{"q": {strconv.FormatUint(tenantID, 10)}}
	path := fmt.Sprintf("/api/v1/organizations/%s/search-tenants", escapePathSegment(orgID))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, q)
	if err != nil {
		return nil, err
	}
	var result struct {
		Data []TenantInviteCandidate `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// SearchUsersForInvite no longer sends a request because organization membership is workspace-based.
//
// Deprecated: use SearchTenantsForInvite with an explicit workspace ID.
func (c *Client) SearchUsersForInvite(_ context.Context, _, _ string) ([]UserInfo, error) {
	return nil, fmt.Errorf(
		"user invitation search is no longer supported; use SearchTenantsForInvite with a workspace ID",
	)
}

// InviteTenantMember enrolls a workspace the caller administers into an organization.
func (c *Client) InviteTenantMember(ctx context.Context, orgID string, tenantID uint64, role string) error {
	if tenantID == 0 {
		return fmt.Errorf("workspace ID must be nonzero")
	}
	req := struct {
		TenantID uint64 `json:"tenant_id"`
		Role     string `json:"role"`
	}{tenantID, role}
	path := fmt.Sprintf("/api/v1/organizations/%s/invite", escapePathSegment(orgID))
	resp, err := c.doRequest(ctx, http.MethodPost, path, req, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// InviteMember uses the legacy server lookup to enroll the workspace belonging to userID.
// The caller must administer that workspace; the user is not added as a representative.
//
// Deprecated: use InviteTenantMember with an explicit workspace ID.
func (c *Client) InviteMember(ctx context.Context, orgID, userID, role string) error {
	req := map[string]string{
		"user_id": userID,
		"role":    role,
	}
	path := fmt.Sprintf("/api/v1/organizations/%s/invite", escapePathSegment(orgID))
	resp, err := c.doRequest(ctx, http.MethodPost, path, req, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// ListOrgMembers lists members of an organization.
func (c *Client) ListOrgMembers(ctx context.Context, orgID string) ([]OrganizationMemberResponse, error) {
	path := fmt.Sprintf("/api/v1/organizations/%s/members", escapePathSegment(orgID))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Members []OrganizationMemberResponse `json:"members"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Members, nil
}

// UpdateTenantMemberRole updates the role of a workspace in an organization.
func (c *Client) UpdateTenantMemberRole(ctx context.Context, orgID string, tenantID uint64, role string) error {
	if tenantID == 0 {
		return fmt.Errorf("workspace ID must be nonzero")
	}
	req := map[string]string{"role": role}
	path := fmt.Sprintf("/api/v1/organizations/%s/members/%d", escapePathSegment(orgID), tenantID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, req, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// UpdateMemberRole refuses to reinterpret a user ID as a workspace ID.
//
// Deprecated: use UpdateTenantMemberRole with an explicit workspace ID.
func (c *Client) UpdateMemberRole(_ context.Context, _, _, _ string) error {
	return fmt.Errorf(
		"user-based organization roles are no longer supported; use UpdateTenantMemberRole with a workspace ID",
	)
}

// RemoveTenantMember removes a workspace from an organization.
func (c *Client) RemoveTenantMember(ctx context.Context, orgID string, tenantID uint64) error {
	if tenantID == 0 {
		return fmt.Errorf("workspace ID must be nonzero")
	}
	path := fmt.Sprintf("/api/v1/organizations/%s/members/%d", escapePathSegment(orgID), tenantID)
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// RemoveMember refuses to reinterpret a user ID as a workspace ID.
//
// Deprecated: use RemoveTenantMember with an explicit workspace ID.
func (c *Client) RemoveMember(_ context.Context, _, _ string) error {
	return fmt.Errorf(
		"user-based organization membership is no longer supported; use RemoveTenantMember with a workspace ID",
	)
}

// --- Join request management ---

// ListJoinRequests lists pending join requests (admin only)
func (c *Client) ListJoinRequests(ctx context.Context, orgID string) ([]JoinRequestResponse, error) {
	path := fmt.Sprintf("/api/v1/organizations/%s/join-requests", escapePathSegment(orgID))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Requests []JoinRequestResponse `json:"requests"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Requests, nil
}

// ReviewJoinRequest reviews a join request (approve/reject)
func (c *Client) ReviewJoinRequest(
	ctx context.Context,
	orgID, requestID string,
	approved bool,
	message, role string,
) error {
	req := map[string]any{
		"approved": approved,
		"message":  message,
		"role":     role,
	}
	path := fmt.Sprintf(
		"/api/v1/organizations/%s/join-requests/%s/review",
		escapePathSegment(orgID),
		escapePathSegment(requestID),
	)
	resp, err := c.doRequest(ctx, http.MethodPut, path, req, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// --- Knowledge base sharing ---

// ShareKnowledgeBase shares a knowledge base with an organization
func (c *Client) ShareKnowledgeBase(
	ctx context.Context,
	kbID, orgID, permission string,
) (*KnowledgeBaseShareResponse, error) {
	req := map[string]string{
		"organization_id": orgID,
		"permission":      permission,
	}
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/shares", escapePathSegment(kbID))
	resp, err := c.doRequest(ctx, http.MethodPost, path, req, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool                        `json:"success"`
		Data    *KnowledgeBaseShareResponse `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ListKBShares lists shares of a knowledge base
func (c *Client) ListKBShares(ctx context.Context, kbID string) ([]KnowledgeBaseShareResponse, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/shares", escapePathSegment(kbID))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Shares []KnowledgeBaseShareResponse `json:"shares"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Shares, nil
}

// UpdateSharePermission updates a KB share's permission
func (c *Client) UpdateSharePermission(ctx context.Context, kbID, shareID, permission string) error {
	req := map[string]string{"permission": permission}
	path := fmt.Sprintf(
		"/api/v1/knowledge-bases/%s/shares/%s",
		escapePathSegment(kbID),
		escapePathSegment(shareID),
	)
	resp, err := c.doRequest(ctx, http.MethodPut, path, req, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// RemoveKBShare removes a KB share
func (c *Client) RemoveKBShare(ctx context.Context, kbID, shareID string) error {
	path := fmt.Sprintf(
		"/api/v1/knowledge-bases/%s/shares/%s",
		escapePathSegment(kbID),
		escapePathSegment(shareID),
	)
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// --- Agent sharing ---

// ShareAgent shares an agent with an organization
func (c *Client) ShareAgent(ctx context.Context, agentID, orgID, permission string) (*AgentShareResponse, error) {
	req := map[string]string{
		"organization_id": orgID,
		"permission":      permission,
	}
	path := fmt.Sprintf("/api/v1/agents/%s/shares", escapePathSegment(agentID))
	resp, err := c.doRequest(ctx, http.MethodPost, path, req, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool                `json:"success"`
		Data    *AgentShareResponse `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ListAgentShares lists shares of an agent
func (c *Client) ListAgentShares(ctx context.Context, agentID string) ([]AgentShareResponse, error) {
	path := fmt.Sprintf("/api/v1/agents/%s/shares", escapePathSegment(agentID))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Shares []AgentShareResponse `json:"shares"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Shares, nil
}

// RemoveAgentShare removes an agent share
func (c *Client) RemoveAgentShare(ctx context.Context, agentID, shareID string) error {
	path := fmt.Sprintf(
		"/api/v1/agents/%s/shares/%s",
		escapePathSegment(agentID),
		escapePathSegment(shareID),
	)
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// --- Organization shared resources ---

// ListOrgShares lists knowledge bases shared to an organization
func (c *Client) ListOrgShares(ctx context.Context, orgID string) ([]KnowledgeBaseShareResponse, error) {
	path := fmt.Sprintf("/api/v1/organizations/%s/shares", escapePathSegment(orgID))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Shares []KnowledgeBaseShareResponse `json:"shares"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Shares, nil
}

// ListOrgAgentShares lists agents shared to an organization
func (c *Client) ListOrgAgentShares(ctx context.Context, orgID string) ([]AgentShareResponse, error) {
	path := fmt.Sprintf("/api/v1/organizations/%s/agent-shares", escapePathSegment(orgID))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Shares []AgentShareResponse `json:"shares"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Shares, nil
}

// ListSharedKnowledgeBases lists all knowledge bases shared to the current user
func (c *Client) ListSharedKnowledgeBases(ctx context.Context) ([]SharedKnowledgeBaseInfo, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/shared-knowledge-bases", nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool                      `json:"success"`
		Data    []SharedKnowledgeBaseInfo `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ListSharedAgents lists all agents shared to the current user
func (c *Client) ListSharedAgents(ctx context.Context) ([]SharedAgentInfo, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/shared-agents", nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool              `json:"success"`
		Data    []SharedAgentInfo `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}
