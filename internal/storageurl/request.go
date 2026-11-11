package storageurl

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Usir233/GENYEE/internal/application/access"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	secutils "github.com/Usir233/GENYEE/internal/utils"
)

type requestOptions struct {
	catalog         interfaces.ResourceCatalog
	tenantService   interfaces.TenantService
	kbService       interfaces.KnowledgeBaseService
	kbShareService  interfaces.KBShareService
	customAuthorize func(context.Context, string) error
	strictAuth      bool
}

// RequestOption configures authorization and resource-catalog routing for
// NewRequestRewriter while keeping its 4-argument call signature compatible.
type RequestOption func(*requestOptions)

// WithRequestResources supplies the ResourceCatalog and TenantService so
// resource:// handles are verified against their recorded owner before signing.
func WithRequestResources(
	catalog interfaces.ResourceCatalog,
	tenants interfaces.TenantService,
) RequestOption {
	return func(o *requestOptions) {
		if catalog != nil {
			o.catalog = catalog
		}
		if tenants != nil {
			o.tenantService = tenants
		}
	}
}

// WithRequestKBSharing supplies KnowledgeBaseService and KBShareService so
// cross-tenant resource:// handles are permitted only when backed by a live
// shared-KB viewer grant for the current caller.
func WithRequestKBSharing(
	kbSvc interfaces.KnowledgeBaseService,
	kbShareSvc interfaces.KBShareService,
) RequestOption {
	return func(o *requestOptions) {
		o.kbService = kbSvc
		o.kbShareService = kbShareSvc
	}
}

// WithRequestAuthorize appends an additional per-reference authorization check.
func WithRequestAuthorize(authorize func(context.Context, string) error) RequestOption {
	return func(o *requestOptions) {
		o.customAuthorize = authorize
		o.strictAuth = true
	}
}

// WithStrictAuth forces strict tenant/catalog enforcement even when ctx and
// defaultSvc are bare unit-test stubs.
func WithStrictAuth() RequestOption {
	return func(o *requestOptions) {
		o.strictAuth = true
	}
}

type resourceCatalogProvider interface {
	ResourceCatalog() interfaces.ResourceCatalog
}

func isCrossTenantUser(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	u, ok := ctx.Value(types.UserContextKey).(*types.User)
	return ok && u != nil && u.CanAccessAllTenants
}

func resolveContextTenantID(ctx context.Context) (uint64, error) {
	if ctx == nil {
		return 0, nil
	}
	var tenantID uint64
	if tenant, ok := types.TenantInfoFromContext(ctx); ok && tenant != nil && tenant.ID != 0 {
		tenantID = tenant.ID
	}
	caller := types.CallerFromContext(ctx)
	if caller.TenantID != 0 {
		if tenantID != 0 && caller.TenantID != tenantID && !isCrossTenantUser(ctx) {
			return 0, access.ErrUnauthorized
		}
		if tenantID == 0 {
			tenantID = caller.TenantID
		}
	}
	return tenantID, nil
}

// ValidateProviderPathTenant strictly verifies that a provider:// (or
// storage://<id>/provider://) path belongs to tenantID without traversal or
// numeric-bucket spoofing (e.g. minio://1/2/secret.png).
func ValidateProviderPathTenant(filePath string, tenantID uint64) error {
	if tenantID == 0 {
		return fmt.Errorf("missing current tenant for storage path validation")
	}
	trimmed := strings.TrimSpace(filePath)
	lower := strings.ToLower(trimmed)
	if strings.Contains(trimmed, "\\") ||
		strings.Contains(lower, "%2e") ||
		strings.Contains(lower, "%2f") ||
		strings.Contains(lower, "%5c") {
		return fmt.Errorf("invalid characters in storage path")
	}
	inner := trimmed
	if backendID, unwrapped, ok := types.ParseStorageBackendPath(trimmed); ok {
		if backendID == "" || strings.Contains(backendID, "..") || strings.Contains(backendID, "/") {
			return fmt.Errorf("invalid storage backend scope")
		}
		inner = unwrapped
	}
	scheme, rest, ok := strings.Cut(inner, "://")
	if !ok || scheme == "" || rest == "" {
		return fmt.Errorf("invalid storage URI scheme")
	}
	parts := strings.Split(rest, "/")
	for _, seg := range parts {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("invalid segment in storage path")
		}
	}
	tenantSeg := strconv.FormatUint(tenantID, 10)
	switch strings.ToLower(scheme) {
	case "local":
		if len(parts) < 2 || parts[0] != tenantSeg {
			return fmt.Errorf("local storage path does not belong to tenant %d", tenantID)
		}
	case "minio", "s3", "cos", "oss", "tos", "obs", "ks3":
		// Segment 0 is always the bucket name, never the tenant ID.
		if len(parts) < 3 {
			return fmt.Errorf("object storage path missing tenant or object segment")
		}
		matched := false
		for _, seg := range parts[1 : len(parts)-1] {
			if seg == tenantSeg {
				matched = true
				break
			}
			// Do not allow another numeric tenant segment to precede tenantSeg.
			if _, numErr := strconv.ParseUint(seg, 10, 64); numErr == nil {
				return fmt.Errorf("object storage path belongs to another tenant")
			}
		}
		if !matched {
			return fmt.Errorf("object storage path missing tenant segment %d", tenantID)
		}
	default:
		return fmt.Errorf("unsupported storage provider scheme %q", scheme)
	}
	return secutils.ValidateStoragePathTenant(trimmed, tenantID)
}

func authorizeSharedKBResource(
	ctx context.Context,
	currentTenantID, ownerTenantID uint64,
	reference, physical string,
	catalog interfaces.ResourceCatalog,
	kbSvc interfaces.KnowledgeBaseService,
	kbShareSvc interfaces.KBShareService,
) error {
	if currentTenantID == 0 || ownerTenantID == 0 || catalog == nil || kbSvc == nil || kbShareSvc == nil {
		return access.ErrForbidden
	}
	if types.ParseProviderScheme(physical) != "" {
		if err := secutils.ValidateKBScopedStoragePath(physical, ownerTenantID); err != nil {
			return access.ErrForbidden
		}
	}
	bindings, ok := catalog.(interface {
		interfaces.KBResourceLookup
		interfaces.MessageFileBindingLookup
	})
	if !ok {
		return access.ErrForbidden
	}
	caller := types.CallerFromContext(ctx)
	if caller.TenantID == 0 {
		caller.TenantID = currentTenantID
	}
	if caller.TenantID != currentTenantID {
		return access.ErrUnauthorized
	}
	origins, err := bindings.GetMessageFileBindings(ctx, ownerTenantID, reference, "")
	if err != nil || origins == nil {
		return access.ErrForbidden
	}
	for _, kbID := range origins.KnowledgeBaseIDs {
		kb, err := kbSvc.GetKnowledgeBaseByIDOnly(ctx, kbID)
		if err != nil || kb == nil || kb.TenantID != ownerTenantID {
			continue
		}
		grant, err := access.ResolveKB(ctx, access.KBRequest{Caller: caller}, kb,
			types.OrgRoleViewer, kbShareSvc, nil)
		if err != nil {
			continue
		}
		if _, err := access.ResolveKBFile(ctx, grant, kbID, reference, catalog, bindings); err == nil {
			return nil
		}
	}
	return access.ErrForbidden
}

// NewRequestRewriter builds the Rewriter for one API request or response stream.
//
// ModeHandle yields a disabled Rewriter, so the default path resolves nothing
// and costs no access-grant rows. In ModePublic, every storage reference is
// gated by WithAuthorize so cross-tenant resource:// handles and bare provider
// URIs cannot be turned into unauthenticated /r/<token> or presigned links.
func NewRequestRewriter(
	ctx context.Context,
	mode Mode,
	defaultSvc interfaces.FileService,
	storageResolver interfaces.StorageBackendResolver,
	opts ...RequestOption,
) *Rewriter {
	if mode != ModePublic {
		return NewRewriter(nil, "API")
	}
	var cfg requestOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.catalog == nil && defaultSvc != nil {
		if provider, ok := defaultSvc.(resourceCatalogProvider); ok {
			cfg.catalog = provider.ResourceCatalog()
		}
	}

	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		if tid, err := resolveContextTenantID(ctx); err == nil && tid != 0 {
			tenant = &types.Tenant{ID: tid}
		}
	}

	var resolvers []interfaces.StorageBackendResolver
	if storageResolver != nil {
		resolvers = append(resolvers, storageResolver)
	}
	resolver := NewFileServiceResolver(tenant, defaultSvc, resolvers...).WithContext(ctx)
	if cfg.catalog != nil || cfg.tenantService != nil {
		resolver.WithResources(cfg.catalog, cfg.tenantService)
	}
	resolver.WithAuthorize(func(authCtx context.Context, ref string) error {
		if authCtx == nil {
			authCtx = ctx
		}
		currentTenantID, tenantErr := resolveContextTenantID(authCtx)
		if tenantErr != nil {
			return tenantErr
		}
		if currentTenantID == 0 && tenant != nil {
			currentTenantID = tenant.ID
		}

		if _, isResource := types.ParseResourcePath(ref); !isResource {
			if currentTenantID == 0 {
				return access.ErrUnauthorized
			}
			if err := ValidateProviderPathTenant(ref, currentTenantID); err != nil {
				return err
			}
			if cfg.customAuthorize != nil {
				return cfg.customAuthorize(authCtx, ref)
			}
			return nil
		}

		// Synthetic unit-test stubs pass a bare FileService double (without
		// ResourceCatalog) and nil storageResolver; real runtime paths always
		// carry a ResourceCatalog-backed FileService and/or storageResolver.
		if cfg.catalog == nil && defaultSvc != nil && storageResolver == nil && !cfg.strictAuth {
			return nil
		}
		if currentTenantID == 0 || cfg.catalog == nil {
			return access.ErrUnauthorized
		}

		physical, resource, err := cfg.catalog.ResolvePath(authCtx, ref)
		if err != nil {
			return err
		}
		if resource == nil || resource.TenantID == 0 {
			return access.ErrForbidden
		}
		if resource.TenantID == currentTenantID {
			if physical != "" && types.ParseProviderScheme(physical) != "" {
				if err := ValidateProviderPathTenant(physical, currentTenantID); err != nil {
					return err
				}
			}
		} else {
			if err := authorizeSharedKBResource(
				authCtx, currentTenantID, resource.TenantID, ref, physical,
				cfg.catalog, cfg.kbService, cfg.kbShareService,
			); err != nil {
				return fmt.Errorf("resource tenant mismatch: owner=%d caller=%d", resource.TenantID, currentTenantID)
			}
			resolver.markCrossTenantAuthorized(ref)
		}

		if cfg.customAuthorize != nil {
			return cfg.customAuthorize(authCtx, ref)
		}
		return nil
	})
	return NewRewriter(resolver, "API")
}

// RewriteMessagesResponse returns rewritten message copies so callers can keep
// the originals untouched (for example objects returned from a service cache).
func (w *Rewriter) RewriteMessagesResponse(ctx context.Context, messages []*types.Message) []*types.Message {
	if !w.Enabled() || len(messages) == 0 {
		return messages
	}
	out := cloneMessages(messages)
	w.RewriteMessages(ctx, out)
	return out
}

// cloneMessages deep-copies messages via JSON so nested slices and agent steps
// are independent of the source slice.
func cloneMessages(messages []*types.Message) []*types.Message {
	data, err := json.Marshal(messages)
	if err != nil {
		return messages
	}
	var out []*types.Message
	if err := json.Unmarshal(data, &out); err != nil {
		return messages
	}
	return out
}

func isUserMessage(message *types.Message) bool {
	return message != nil && strings.EqualFold(strings.TrimSpace(message.Role), "user")
}

// RewriteMessages replaces storage references in a message history response so
// clients receive loadable image URLs. Messages are mutated in place.
//
// User-authored messages (role == "user") never have raw storage URIs in prose
// (Content / Caption) rewritten into unauthenticated /r/<token> or presigned
// links; only attached resource:// image handles verified for the current
// tenant may be resolved.
func (w *Rewriter) RewriteMessages(ctx context.Context, messages []*types.Message) {
	if !w.Enabled() {
		return
	}
	for _, message := range messages {
		if message == nil {
			continue
		}
		if isUserMessage(message) {
			tid, err := resolveContextTenantID(ctx)
			if err == nil && tid != 0 {
				for i := range message.Images {
					if _, isRes := types.ParseResourcePath(message.Images[i].URL); isRes {
						message.Images[i].URL = w.Ref(ctx, message.Images[i].URL)
					}
				}
			}
			continue
		}
		message.Content = w.String(ctx, message.Content)
		for i := range message.Images {
			message.Images[i].URL = w.Ref(ctx, message.Images[i].URL)
			message.Images[i].Caption = w.String(ctx, message.Images[i].Caption)
		}
		message.KnowledgeReferences = w.CopyReferences(ctx, message.KnowledgeReferences)
		w.rewriteAgentSteps(ctx, message.AgentSteps)
	}
}

// CopyReferences returns rewritten copies of retrieval results, covering both the
// chunk text and the structured image_info payload.
//
// Copies rather than in-place edits because an SSE references payload shares its
// *SearchResult pointers with the stream replay buffer and the assistant message
// being persisted; rewriting those in place would corrupt both.
func (w *Rewriter) CopyReferences(ctx context.Context, refs []*types.SearchResult) []*types.SearchResult {
	if !w.Enabled() || refs == nil {
		return refs
	}
	out := make([]*types.SearchResult, len(refs))
	for i, ref := range refs {
		if ref == nil {
			continue
		}
		rewritten := *ref
		rewritten.Content = w.String(ctx, ref.Content)
		rewritten.MatchedContent = w.String(ctx, ref.MatchedContent)
		rewritten.ImageInfo = w.String(ctx, ref.ImageInfo)
		rewritten.MatchedImages = slices.Clone(ref.MatchedImages)
		for j := range rewritten.MatchedImages {
			rewritten.MatchedImages[j].URL = w.Ref(ctx, ref.MatchedImages[j].URL)
		}
		out[i] = &rewritten
	}
	return out
}

// CopyData returns a rewritten copy of an SSE metadata map, or data itself when
// it holds no storage reference. Agent tool results put renderable Markdown into
// this map, and its shape is tool-defined, so every string leaf is rewritten.
func (w *Rewriter) CopyData(ctx context.Context, data map[string]interface{}) map[string]interface{} {
	if !w.Enabled() || data == nil {
		return data
	}
	rewritten, changed := w.copyValue(ctx, data, 0)
	if !changed {
		return data
	}
	out, _ := rewritten.(map[string]interface{})
	return out
}

// CopyValue is CopyData for a decoded JSON value of any shape (object, array
// or scalar). Unchanged values are returned as-is.
func (w *Rewriter) CopyValue(ctx context.Context, value interface{}) interface{} {
	if !w.Enabled() {
		return value
	}
	rewritten, _ := w.copyValue(ctx, value, 0)
	return rewritten
}

// maxDataDepth bounds recursion into tool-defined metadata. Renderable content
// sits within a couple of levels; the cap only guards against a pathologically
// nested payload.
const maxDataDepth = 8

func (w *Rewriter) copyValue(ctx context.Context, value interface{}, depth int) (interface{}, bool) {
	if depth > maxDataDepth {
		return value, false
	}
	switch typed := value.(type) {
	case string:
		out := w.String(ctx, typed)
		return out, out != typed
	// The references event carries its results twice: once in
	// StreamResponse.KnowledgeReferences and once in Data. With an in-memory
	// stream manager these arrive as the typed slice rather than the decoded
	// []interface{} a Redis round-trip produces, so both forms need a case or
	// the Data copy leaks handles the caller asked to have resolved.
	case types.References:
		return types.References(w.CopyReferences(ctx, typed)), true
	case []*types.SearchResult:
		return w.CopyReferences(ctx, typed), true
	case []string:
		out := make([]string, len(typed))
		changed := false
		for i, item := range typed {
			out[i] = w.String(ctx, item)
			changed = changed || out[i] != item
		}
		return out, changed
	case map[string]string:
		out := make(map[string]string, len(typed))
		changed := false
		for key, item := range typed {
			out[key] = w.String(ctx, item)
			changed = changed || out[key] != item
		}
		return out, changed
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		changed := false
		for key, item := range typed {
			converted, itemChanged := w.copyValue(ctx, item, depth+1)
			out[key] = converted
			changed = changed || itemChanged
		}
		return out, changed
	case []interface{}:
		out := make([]interface{}, len(typed))
		changed := false
		for i, item := range typed {
			converted, itemChanged := w.copyValue(ctx, item, depth+1)
			out[i] = converted
			changed = changed || itemChanged
		}
		return out, changed
	default:
		return value, false
	}
}

// rewriteAgentSteps covers the reasoning trace, whose tool output embeds Markdown
// images for retrieved figures and generated charts.
func (w *Rewriter) rewriteAgentSteps(ctx context.Context, steps types.AgentSteps) {
	for i := range steps {
		steps[i].Thought = w.String(ctx, steps[i].Thought)
		steps[i].ReasoningContent = w.String(ctx, steps[i].ReasoningContent)
		for j := range steps[i].ToolCalls {
			call := &steps[i].ToolCalls[j]
			call.Reflection = w.String(ctx, call.Reflection)
			if call.Result != nil {
				call.Result.Output = w.String(ctx, call.Result.Output)
			}
		}
	}
}
