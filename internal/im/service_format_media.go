package im

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	agenttools "github.com/Usir233/GENYEE/internal/agent/tools"
	"github.com/Usir233/GENYEE/internal/event"
	"github.com/Usir233/GENYEE/internal/infrastructure/docparser"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/storageurl"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/Usir233/GENYEE/internal/utils"
)

const (
	imNoAnswerFallback  = "抱歉，我暂时无法回答这个问题。"
	imErrorFallback     = "抱歉，处理您的问题时出现了异常，请稍后再试。"
	imCancelledFallback = "抱歉，回答已被取消。"

	// dedupTTL is how long processed message IDs are retained.
	dedupTTL = 5 * time.Minute
	// dedupCleanupInterval is how often the dedup map is cleaned.
	dedupCleanupInterval = 1 * time.Minute
	// maxContentLength is the maximum allowed message content length.
	maxContentLength = 4096
	// maxQuoteContentLength is the max runes to include from a quoted message.
	maxQuoteContentLength = 500
	// maxIMAttachmentBytes bounds an attachment buffered for IM Q&A.
	maxIMAttachmentBytes = 32 << 20 // 32 MiB
	// maxIMVisionAttachmentBytes prevents large images from expanding into an oversized data URI.
	maxIMVisionAttachmentBytes = 8 << 20 // 8 MiB
	// maxIMAttachmentLines matches the legacy attachment prompt limit.
	maxIMAttachmentLines = 500
	// maxIMAttachmentContentBytes bounds parsed text persisted in the IM message and injected into QA.
	maxIMAttachmentContentBytes = 32 << 10 // 32 KiB
	// imAttachmentReadTimeout bounds downloading and parsing before the QA request runs.
	imAttachmentReadTimeout = time.Minute
	// streamFlushInterval is how often buffered stream content is flushed to the IM platform.
	// This prevents API rate-limiting while keeping perceived latency low.
	streamFlushInterval = 300 * time.Millisecond
	// agentCompleteWaitTimeout bounds how long IM waits for EventAgentComplete after
	// the final answer stream finishes, preventing indefinite hangs.
	agentCompleteWaitTimeout = 10 * time.Second
)

// imCitationTagRe matches inline citation tags produced by the agent pipeline.
// These tags are rendered as interactive UI in the web frontend but are meaningless
// in IM platforms, so they must be stripped before sending.
var imCitationTagRe = regexp.MustCompile(`<(?:kb|web)\b[^>]*/?>`)

// stripIMCitationTags removes <kb .../> and <web .../> inline citation tags from s.
func stripIMCitationTags(s string) string {
	return imCitationTagRe.ReplaceAllString(s, "")
}

// imageXMLBlockRe matches <image ...>...</image> blocks produced by
// EnrichContentWithImageInfo in the RAG context pipeline. These blocks contain
// metadata for the LLM and must be stripped before sending to IM platforms.
var imageXMLBlockRe = regexp.MustCompile(`(?s)<image\b[^>]*>.*?</image>`)

// imageOriginalRe extracts the original markdown image syntax from <image_original> tags.
var imageOriginalRe = regexp.MustCompile(`<image_original>(.*?)</image_original>`)

// stripImageXMLTags collapses <image> blocks back to plain markdown.
// Extracts the original ![alt](url) from <image_original> when present,
// otherwise drops the block entirely.
func stripImageXMLTags(s string) string {
	return imageXMLBlockRe.ReplaceAllStringFunc(s, func(block string) string {
		if m := imageOriginalRe.FindStringSubmatch(block); len(m) > 1 {
			return m[1]
		}
		return ""
	})
}

// rewriteStorageURLs replaces all storage references in content with HTTP URLs
// so IM clients — which cannot attach GENYEE credentials to an image fetch —
// can render them. See internal/storageurl for the shared implementation.
func rewriteStorageURLs(ctx context.Context, content string, resolver *storageurl.FileServiceResolver) string {
	if resolver == nil {
		return content
	}
	return storageurl.Rewrite(ctx, content, resolver, "IM")
}

// ── Streaming holdback helpers ──
// During streaming, content is flushed in 300ms batches. A storage reference or
// an XML tag may be split across two batches. These helpers detect incomplete
// patterns at the end of a chunk so the caller can hold them back until the
// next flush completes them.

// incompleteXMLTagRe matches the opening of an <image…>, <kb…>, or <web…> tag
// that reaches the end of the string without a closing '>'.
var incompleteXMLTagRe = regexp.MustCompile(
	`<(?:image|image_original|image_caption|image_ocr|kb|web)[^>]*$`,
)

// findIncompleteXMLTag returns the byte offset of a potentially truncated XML
// tag at the tail of s, or -1 if none.
func findIncompleteXMLTag(s string) int {
	loc := incompleteXMLTagRe.FindStringIndex(s)
	if loc == nil {
		return -1
	}
	return loc[0]
}

// holdbackCutoff returns the earliest incomplete-pattern offset at the tail of
// chunk, or len(chunk) if the chunk is safe to flush entirely.
func holdbackCutoff(chunk string) int {
	cutoff := storageurl.HoldbackCutoff(chunk)
	if idx := findIncompleteXMLTag(chunk); idx >= 0 && idx < cutoff {
		cutoff = idx
	}
	return cutoff
}

// formatIMOutboundAnswer strips thinking/tool blocks and applies IM content cleanup.
func formatIMOutboundAnswer(ctx context.Context, raw string, resolver *storageurl.FileServiceResolver) string {
	return cleanIMContent(ctx, FormatIMDisplayContent(raw, StreamDisplayFinal), resolver)
}

// formatIMOutboundAnswerOrFallback guarantees that cleanup cannot turn a
// non-empty model payload (for example, a think-only response) into an empty IM
// message. Callers may pass imErrorFallback or imCancelledFallback as raw when
// QA itself failed or was stopped.
func formatIMOutboundAnswerOrFallback(
	ctx context.Context,
	raw string,
	resolver *storageurl.FileServiceResolver,
) string {
	content := formatIMOutboundAnswer(ctx, raw, resolver)
	if strings.TrimSpace(content) == "" {
		return imNoAnswerFallback
	}
	return content
}

// imOutboundContext keeps values from ctx but drops cancellation. /stop cancels
// the QA request; Feishu CardKit calls honor that deadline and would otherwise
// leave the thinking placeholder on screen.
func imOutboundContext(ctx context.Context) context.Context {
	return context.WithoutCancel(ctx)
}

// imQAFailureReply maps a QA error onto the user-visible IM fallback text.
func imQAFailureReply(err error) string {
	if err == nil {
		return imNoAnswerFallback
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return imCancelledFallback
	}
	return imErrorFallback
}

// cleanIMContent applies all IM-specific content transformations:
//  1. Collapse <image> XML blocks back to plain markdown
//  2. Strip <kb/> and <web/> citation tags
//  3. Rewrite storage references to HTTP URLs using their owning storage instance
func cleanIMContent(ctx context.Context, content string, resolver *storageurl.FileServiceResolver) string {
	content = stripImageXMLTags(content)
	content = stripIMCitationTags(content)
	if resolver != nil {
		resolver.WithContext(ctx)
	}
	content = rewriteStorageURLs(ctx, content, resolver)
	return content
}

// newIMFileServiceResolver builds a per-message storage backend resolver. The
// cache lives for one cleanIMContent / outbound message so a long answer does
// not re-create an SDK client for every reference.
func newIMFileServiceResolver(
	tenant *types.Tenant,
	defaultSvc interfaces.FileService,
	storageResolvers ...interfaces.StorageBackendResolver,
) *storageurl.FileServiceResolver {
	return storageurl.NewFileServiceResolver(tenant, defaultSvc, storageResolvers...)
}

// Every platform and output mode uses the same resource-aware resolver. Keep
// it scoped to the reply so shared KB images select their owner's backend and
// multiple images on the same instance reuse its client during streaming.
func (s *Service) newOutboundFileServiceResolver(
	tenant *types.Tenant,
	agent *types.CustomAgent,
	kbIDs []string,
) *storageurl.FileServiceResolver {
	return newIMFileServiceResolver(tenant, s.defaultFileSvc, s.storageResolver).
		WithResources(s.resourceCatalog, s.tenantService).
		WithAuthorization(func(ctx context.Context, reference string) error {
			return s.authorizeOutboundResource(ctx, tenant, agent, kbIDs, reference)
		})
}

func buildIMFileServiceForProvider(
	tenant *types.Tenant,
	provider string,
	defaultSvc interfaces.FileService,
) interfaces.FileService {
	return storageurl.BuildFileServiceForProvider(tenant, provider, defaultSvc)
}

// resolveIMFileServiceForPath is a test/helper entry point without caching.
func resolveIMFileServiceForPath(
	tenant *types.Tenant,
	filePath string,
	defaultSvc interfaces.FileService,
) interfaces.FileService {
	return newIMFileServiceResolver(tenant, defaultSvc).ResolveFileService(filePath)
}

// nonTextTypeLabel maps a message type to a Chinese label for LLM instructions.
var nonTextTypeLabel = map[string]string{
	"image": "图片",
	"file":  "文件",
	"video": "视频",
	"voice": "语音",
}

// formatQuotedContext formats a QuotedMessage into a labeled string for LLM context.
// Returns empty string if quote is nil.
// For non-text quotes, generates an instruction telling the LLM to acknowledge
// the unprocessable content instead of a placeholder that causes hallucination.
func formatQuotedContext(quote *QuotedMessage) string {
	if quote == nil {
		return ""
	}
	// Non-text quote: generate instruction, not content placeholder.
	if quote.NonTextType != "" {
		label := nonTextTypeLabel[quote.NonTextType]
		if label == "" {
			label = "该类型的"
		}
		return "用户引用了一条" + label + "消息，但你无法查看该内容。请直接告知用户你目前无法处理" + label + "消息，建议用户用文字描述问题。不要猜测该消息的内容。"
	}
	if quote.Content == "" {
		return ""
	}
	content := quote.Content
	runes := []rune(content)
	if len(runes) > maxQuoteContentLength {
		content = string(runes[:maxQuoteContentLength]) + "..."
	}
	// Prevent quoted content from escaping the XML tag boundary.
	content = strings.ReplaceAll(content, "</quoted_message>", "")
	label := "以下是用户引用的一条历史消息，仅作为上下文参考："
	if quote.IsBotMessage {
		label = "以下是用户引用的你（机器人）之前的回复，仅作为上下文参考："
	}
	return label + "\n<quoted_message>\n" + content + "\n</quoted_message>"
}

// withIMIdentity injects a synthetic caller identity into the context for IM
// callbacks. IM platforms verify their own signatures and bypass the auth
// middleware, so the downstream QA pipeline would otherwise see an empty
// UserID/TenantRole. Mirroring the API-key path's "system-<tenantID>" synthetic
// user (recognised by types.IsSyntheticUserID) lets Organization-shared
// knowledge bases be merged and resolved correctly, since the shared-KB code
// gates on a non-empty UserID. Viewer is the least privilege sufficient to
// retrieve shared KBs. The channel locale replaces any HTTP middleware value
// because webhook Accept-Language belongs to the IM platform, not the writer;
// channels without an explicit locale use the deployment default.
func withIMIdentity(ctx context.Context, channel *IMChannel, msg *IncomingMessage) context.Context {
	tenantID := channel.TenantID
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, types.UserIDContextKey, fmt.Sprintf("system-%d", tenantID))
	locale := channel.Locale
	if locale == "" {
		locale = types.DefaultLanguage()
	}
	ctx = context.WithValue(ctx, types.LanguageContextKey, locale)
	if msg != nil {
		principalID := fmt.Sprintf("%d:%s:%s:%s", tenantID, channel.ID, msg.Platform, msg.UserID)
		ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalIMUser, ID: principalID})
		ctx = types.WithPrincipalAuthSource(ctx, types.AuthSourceIM)
	}
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleViewer)
	// IM bots have no live client that can complete an in-conversation MCP OAuth
	// prompt, so mark the context non-interactive: the agent emits a one-shot
	// authorization notice (surfaced in the reply) instead of blocking until the
	// OAuth wait times out for every unauthorized service.
	ctx = types.WithMCPOAuthNonInteractive(ctx)
	return ctx
}

// imMCPAuthService identifies an OAuth-enabled MCP service that the IM user has
// not authorized yet, collected during a turn so an authorization notice can be
// appended to the reply.
type imMCPAuthService struct {
	ID   string
	Name string
}

// mcpOAuthCallbackURL returns the absolute backend callback URL registered with
// the authorization server, derived from APP_EXTERNAL_URL. Empty when unset.
func mcpOAuthCallbackURL() string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("APP_EXTERNAL_URL")), "/")
	if base == "" {
		return ""
	}
	return base + "/api/v1/mcp-oauth/callback"
}

// buildIMMCPAuthNotice builds a user-facing authorization notice for the given
// OAuth-enabled MCP services. When the OAuth manager and APP_EXTERNAL_URL are
// available it generates a per-service authorization URL (StartAuthorization)
// the user can open; otherwise it falls back to a console hint. Returns "" when
// there is nothing to report. The user authorizes out-of-band, then re-sends
// their message to use the service (IM cannot resolve the prompt inline).
func (s *Service) buildIMMCPAuthNotice(ctx context.Context, services []imMCPAuthService) string {
	// Deduplicate by service ID, preserving order.
	seen := make(map[string]bool, len(services))
	uniq := make([]imMCPAuthService, 0, len(services))
	for _, svc := range services {
		if svc.ID == "" || seen[svc.ID] {
			continue
		}
		seen[svc.ID] = true
		uniq = append(uniq, svc)
	}
	if len(uniq) == 0 {
		return ""
	}

	tenantID, _ := types.TenantIDFromContext(ctx)
	principal := types.MCPOAuthPrincipalFromContext(ctx)
	redirectURI := mcpOAuthCallbackURL()
	frontendRedirect := strings.TrimSpace(os.Getenv("APP_EXTERNAL_URL"))
	if frontendRedirect == "" {
		frontendRedirect = "/"
	}

	var lines []string
	for _, svc := range uniq {
		name := strings.TrimSpace(svc.Name)
		if name == "" {
			name = svc.ID
		}
		var authURL string
		if s.oauthManager != nil && redirectURI != "" && tenantID != 0 && principal.Valid() {
			url, err := s.oauthManager.StartAuthorizationForService(
				ctx, tenantID, principal, svc.ID, redirectURI, frontendRedirect,
			)
			if err != nil {
				logger.Warnf(ctx, "[IM] Failed to build MCP OAuth URL for service %s: %v", svc.ID, err)
			} else {
				authURL = url
			}
		}
		if authURL != "" {
			lines = append(lines, fmt.Sprintf("• %s：%s", name, authURL))
		} else {
			lines = append(lines, fmt.Sprintf("• %s（请在 GENYEE 管理后台完成 OAuth 授权）", name))
		}
	}

	return "⚠️ 以下 MCP 服务需要授权后才能使用，请点击链接完成授权，然后重新发送你的消息：\n" +
		strings.Join(lines, "\n")
}

// appendIMAuthNotice appends an authorization notice to an existing reply body,
// separated by a blank line. When the body is empty the notice becomes the body.
func appendIMAuthNotice(body, notice string) string {
	if notice == "" {
		return body
	}
	if strings.TrimSpace(body) == "" {
		return notice
	}
	return body + "\n\n" + notice
}

func buildIMQARequest(
	session *types.Session,
	query string,
	assistantMessageID string,
	userMessageID string,
	customAgent *types.CustomAgent,
	kbIDs []string,
	quote *QuotedMessage,
	attachments ...types.MessageAttachments,
) *types.QARequest {
	// WebSearchEnabled: the web handler passes this per-request from the
	// frontend toggle; for IM channels the user has no per-message toggle,
	// so we derive it from the agent config (the single source of truth).
	webSearchEnabled := customAgent != nil && customAgent.Config.WebSearchEnabled
	quotedContext := formatQuotedContext(quote)
	var requestAttachments types.MessageAttachments
	if len(attachments) > 0 {
		requestAttachments = attachments[0]
	}
	return &types.QARequest{
		Session:            session,
		Query:              query,
		AssistantMessageID: assistantMessageID,
		CustomAgent:        customAgent,
		KnowledgeBaseIDs:   kbIDs,
		UserMessageID:      userMessageID,
		WebSearchEnabled:   webSearchEnabled,
		QuotedContext:      quotedContext,
		Attachments:        requestAttachments,
	}
}

func buildIMLastRequestState(
	agentID string,
	customAgent *types.CustomAgent,
	kbIDs []string,
) *types.SessionLastRequestState {
	state := &types.SessionLastRequestState{
		AgentID:          agentID,
		KnowledgeBaseIDs: append([]string(nil), kbIDs...),
	}
	if customAgent == nil {
		return state
	}
	if state.AgentID == "" {
		state.AgentID = customAgent.ID
	}
	state.AgentEnabled = customAgent.IsAgentMode()
	state.ModelID = customAgent.Config.ModelID
	state.WebSearchEnabled = customAgent.Config.WebSearchEnabled
	if len(state.KnowledgeBaseIDs) == 0 && len(customAgent.Config.KnowledgeBases) > 0 {
		state.KnowledgeBaseIDs = append([]string(nil), customAgent.Config.KnowledgeBases...)
	}
	return state
}

func createIMUserMessagePayload(
	sessionID, content, requestID string,
	attachments ...types.MessageAttachments,
) *types.Message {
	var messageAttachments types.MessageAttachments
	if len(attachments) > 0 {
		messageAttachments = attachments[0]
	}
	return &types.Message{
		SessionID:   sessionID,
		Role:        "user",
		Content:     content,
		RequestID:   requestID,
		CreatedAt:   time.Now(),
		IsCompleted: true,
		Channel:     "im",
		Attachments: messageAttachments,
	}
}

type imDownloadedAttachment struct {
	fileName string
	content  []byte
}

// prepareIMAttachments downloads an IM attachment and exposes its parsed text
// (and, for images, a bounded data URI) to the QA pipeline. This is separate
// from the optional background knowledge-base save.
func (s *Service) prepareIMAttachments(
	ctx context.Context,
	msg *IncomingMessage,
	adapter Adapter,
) (types.MessageAttachments, []string, *imDownloadedAttachment, error) {
	if msg.MessageType != MessageTypeFile && msg.MessageType != MessageTypeImage {
		return nil, nil, nil, nil
	}
	if msg.FileSize > maxIMAttachmentBytes {
		return nil, nil, nil, fmt.Errorf("attachment exceeds the %d MiB limit", maxIMAttachmentBytes>>20)
	}
	downloader, ok := adapter.(FileDownloader)
	if !ok {
		return nil, nil, nil, fmt.Errorf("platform %s does not support attachment download", msg.Platform)
	}
	attachmentCtx, cancel := context.WithTimeout(ctx, imAttachmentReadTimeout)
	defer cancel()
	reader, fileName, err := downloader.DownloadFile(attachmentCtx, msg)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() { _ = reader.Close() }()
	content, err := io.ReadAll(io.LimitReader(reader, maxIMAttachmentBytes+1))
	if err != nil {
		return nil, nil, nil, err
	}
	if len(content) > maxIMAttachmentBytes {
		return nil, nil, nil, fmt.Errorf("attachment exceeds the %d MiB limit", maxIMAttachmentBytes>>20)
	}
	if fileName == "" {
		fileName = msg.FileName
	}
	if msg.MessageType == MessageTypeImage && filepath.Ext(fileName) == "" {
		fileName += ".png"
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fileName), "."))
	if ext == "" {
		return nil, nil, nil, fmt.Errorf("attachment has no file extension")
	}
	attachment := types.MessageAttachment{FileName: fileName, FileType: "." + ext, FileSize: int64(len(content))}
	request := &types.ReadRequest{FileContent: content, FileName: fileName, FileType: ext}
	var result *types.ReadResult
	isImage := msg.MessageType == MessageTypeImage || docparser.IsImageFormat(ext)
	if isImage && s.documentReader != nil {
		result, err = s.documentReader.Read(attachmentCtx, request)
		if err != nil {
			logger.Warnf(ctx, "[IM] image OCR/document parsing failed, continuing with vision input: %v", err)
			result, err = nil, nil
		}
	}
	if result == nil && docparser.IsSimpleFormat(attachment.FileType) {
		result, err = (&docparser.SimpleFormatReader{}).Read(attachmentCtx, request)
	} else if result == nil && !isImage && s.documentReader != nil {
		result, err = s.documentReader.Read(attachmentCtx, request)
	}
	if err != nil {
		logger.Warnf(ctx, "[IM] attachment parsing failed, continuing with attachment metadata: %v", err)
	}
	if result != nil {
		applyIMAttachmentTruncation(result.MarkdownContent, &attachment)
	}
	var imageURLs []string
	if isImage && len(content) <= maxIMVisionAttachmentBytes {
		mediaType := http.DetectContentType(content)
		if !strings.HasPrefix(mediaType, "image/") {
			return nil, nil, nil, fmt.Errorf("invalid image content type: %s", mediaType)
		}
		imageURLs = []string{"data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(content)}
	} else if isImage {
		logger.Warnf(
			ctx,
			"[IM] image is too large for direct vision input: size=%d limit=%d",
			len(content), maxIMVisionAttachmentBytes,
		)
	}
	downloaded := &imDownloadedAttachment{fileName: fileName, content: content}
	return types.MessageAttachments{attachment}, imageURLs, downloaded, nil
}

func applyIMAttachmentTruncation(content string, attachment *types.MessageAttachment) {
	attachment.LineCount = strings.Count(content, "\n") + 1

	limited := truncateUTF8ByBytes(content, maxIMAttachmentContentBytes)
	lines := strings.SplitN(limited, "\n", maxIMAttachmentLines+1)
	if len(lines) > maxIMAttachmentLines {
		limited = strings.Join(lines[:maxIMAttachmentLines], "\n")
	}

	attachment.Content = limited
	attachment.IsTruncated = len(limited) < len(content)
}

func truncateUTF8ByBytes(content string, maxBytes int) string {
	if len(content) <= maxBytes {
		return content
	}

	end := maxBytes
	for end > 0 && !utf8.RuneStart(content[end]) {
		end--
	}
	return content[:end]
}

func createIMAssistantMessagePayload(sessionID, requestID string) *types.Message {
	return &types.Message{
		SessionID:   sessionID,
		Role:        "assistant",
		RequestID:   requestID,
		CreatedAt:   time.Now(),
		IsCompleted: false,
		Channel:     "im",
	}
}

func collectIMKnowledgeReferences(dst *[]*types.SearchResult, refs interface{}) {
	switch v := refs.(type) {
	case []*types.SearchResult:
		*dst = append(*dst, v...)
	case []interface{}:
		for _, ref := range v {
			if sr, ok := ref.(*types.SearchResult); ok {
				*dst = append(*dst, sr)
			}
		}
	}
}

func sanitizeIMAgentSteps(raw interface{}) types.AgentSteps {
	switch steps := raw.(type) {
	case []types.AgentStep:
		return types.AgentSteps(agenttools.SanitizeAgentStepsForStorage(steps))
	case types.AgentSteps:
		return types.AgentSteps(agenttools.SanitizeAgentStepsForStorage([]types.AgentStep(steps)))
	default:
		return nil
	}
}

func applyIMCompleteDataToMessage(msg *types.Message, data event.AgentCompleteData) {
	if msg == nil {
		return
	}
	if data.MessageID != "" && data.MessageID != msg.ID {
		return
	}
	msg.IsCompleted = true
	msg.AgentDurationMs = data.TotalDurationMs
	if usage, ok := data.Usage.(*types.TokenUsage); ok && usage != nil {
		msg.Usage = usage
	}
	if len(data.KnowledgeRefs) > 0 {
		refs := make([]*types.SearchResult, 0, len(data.KnowledgeRefs))
		collectIMKnowledgeReferences(&refs, data.KnowledgeRefs)
		if len(refs) > 0 {
			msg.KnowledgeReferences = types.References(refs)
		}
	}
	if steps := sanitizeIMAgentSteps(data.AgentSteps); len(steps) > 0 {
		msg.AgentSteps = steps
	}
}

// waitForIMAgentComplete blocks until EventAgentComplete, ctx cancellation, or timeout.
func waitForIMAgentComplete(ctx context.Context, completeDone <-chan struct{}, sessionID string) {
	timer := time.NewTimer(agentCompleteWaitTimeout)
	defer timer.Stop()
	select {
	case <-completeDone:
	case <-ctx.Done():
		logger.Warnf(ctx, "[IM] QA context ended before agent complete event: session=%s", sessionID)
	case <-timer.C:
		logger.Warnf(ctx, "[IM] Timed out waiting for agent complete event: session=%s", sessionID)
	}
}

// pickIMStoredAnswer returns the best available answer text from IM stream buffers.
func pickIMStoredAnswer(candidates ...string) string {
	for _, s := range candidates {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// mergeIMAgentAnswerBuffers copies optimistic/live answers into persistence buffers
// when EventAgentFinalAnswer did not populate answerBuilder (e.g. cancel before complete).
func mergeIMAgentAnswerBuffers(answerBuilder, answerOuter, agentLiveAnswer *strings.Builder, completeFinal string) {
	if answerBuilder.Len() > 0 {
		return
	}
	switch {
	case agentLiveAnswer.Len() > 0:
		live := agentLiveAnswer.String()
		answerBuilder.WriteString(live)
		if answerOuter.Len() == 0 {
			answerOuter.WriteString(live)
		}
	case answerOuter.Len() > 0:
		answerBuilder.WriteString(answerOuter.String())
	case strings.TrimSpace(completeFinal) != "":
		answerBuilder.WriteString(completeFinal)
		answerOuter.WriteString(completeFinal)
	}
}

// ── File message handling ──────────────────────────────────────────────
// These methods handle file messages received via IM platforms.
// Files are downloaded from the IM platform, validated, and saved to the
// configured knowledge base asynchronously. The user receives a notification
// at the start and end of processing.
// ────────────────────────────────────────────────────────────────────────

// fileMessageQAContent turns a file-only platform event into a valid QA query.
// IM adapters intentionally leave Content empty for file/image messages, while
// the QA API requires a non-empty query. Preserve a caption when an adapter
// provides one; otherwise identify the uploaded file without claiming that its
// contents have already been read.
func fileMessageQAContent(msg *IncomingMessage) string {
	if strings.TrimSpace(msg.Content) != "" {
		return msg.Content
	}
	fileName := strings.TrimSpace(msg.FileName)
	if fileName == "" {
		fileName = "未命名文件"
	}
	return fmt.Sprintf("我上传了文件「%s」。请确认已收到，并告知我接下来可以如何协助。", fileName)
}

// processDownloadedFileToKnowledgeBase stores bytes already downloaded for QA.
// It deliberately has no user-facing notifications: the originating file message
// receives exactly its normal QA reply, while persistence remains background work.
func (s *Service) processDownloadedFileToKnowledgeBase(
	ctx context.Context,
	channel *IMChannel,
	file *imDownloadedAttachment,
) {
	kbID := channel.KnowledgeBaseID
	tenantID := channel.TenantID

	// Build context with tenant info for the knowledge service
	tenant, err := s.tenantService.GetTenantByID(ctx, tenantID)
	if err != nil {
		logger.Errorf(ctx, "[IM] Failed to get tenant %d for file processing: %v", tenantID, err)
		return
	}
	kbCtx := context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	kbCtx = context.WithValue(kbCtx, types.TenantInfoContextKey, tenant)

	fileName := file.fileName
	ext := fileExtension(fileName)
	// Share the upload policy so IM does not silently discard supported formats.
	if !utils.IsSupportedImportExtension(ext) {
		logger.Infof(ctx, "[IM] Unsupported file type after download: %s (file=%s)", ext, fileName)
		return
	}

	// Create a multipart.FileHeader compatible wrapper
	fh := newInMemoryFileHeader(fileName, file.content)

	// Create knowledge entry via the knowledge service
	knowledge, err := s.knowledgeService.CreateKnowledgeFromFile(
		kbCtx, kbID, fh, nil, nil, "", nil, imPlatformToChannel(channel.Platform), nil,
	)
	if err != nil {
		errMsg := err.Error()
		// Check for duplicate file
		if strings.Contains(errMsg, "duplicate") || strings.Contains(errMsg, "already exists") {
			logger.Infof(ctx, "[IM] File already exists in knowledge base: %s", fileName)
			return
		}
		logger.Errorf(ctx, "[IM] Failed to create knowledge from file: %v", err)
		return
	}

	logger.Infof(ctx, "[IM] File saved to knowledge base: kb=%s knowledge=%s file=%s", kbID, knowledge.ID, fileName)
}

// fileExtension extracts the lowercase file extension from a filename.
func fileExtension(filename string) string {
	parts := strings.Split(filename, ".")
	if len(parts) < 2 {
		return ""
	}
	return strings.ToLower(parts[len(parts)-1])
}

// imPlatformToChannel maps an IM platform identifier to a Knowledge.Channel constant.
func imPlatformToChannel(platform string) string {
	switch strings.ToLower(platform) {
	case "wechat":
		return types.ChannelWechat
	case "wecom", "wxwork":
		return types.ChannelWecom
	case "feishu", "lark":
		return types.ChannelFeishu
	case "dingtalk":
		return types.ChannelDingtalk
	case "slack":
		return types.ChannelSlack
	default:
		return types.ChannelIM
	}
}

// newInMemoryFileHeader wraps in-memory file content as a *multipart.FileHeader
// so it can be passed to CreateKnowledgeFromFile which expects a multipart upload.
func newInMemoryFileHeader(filename string, data []byte) *multipart.FileHeader {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// CreateFormFile escapes the filename for Content-Disposition. Building
	// the header by string concatenation left quotes unescaped, so an
	// attachment such as `Q4 "Final".md` parsed back as an unreadable header.
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		// Fallback: return a minimal FileHeader
		return &multipart.FileHeader{Filename: filename, Size: int64(len(data))}
	}
	_, _ = part.Write(data)
	_ = writer.Close()

	// Parse the multipart body to extract the FileHeader
	reader := multipart.NewReader(body, writer.Boundary())
	form, err := reader.ReadForm(int64(len(data)) + 1024)
	if err != nil || form == nil {
		return &multipart.FileHeader{Filename: filename, Size: int64(len(data))}
	}
	files := form.File["file"]
	if len(files) == 0 {
		return &multipart.FileHeader{Filename: filename, Size: int64(len(data))}
	}
	return files[0]
}
