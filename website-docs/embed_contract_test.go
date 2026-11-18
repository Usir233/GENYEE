package websitedocs_test

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	websitedocs "github.com/Usir233/GENYEE/website-docs"
)

func readEmbeddedPage(t *testing.T, path string) string {
	t.Helper()
	raw, err := fs.ReadFile(websitedocs.Pages, path)
	if err != nil {
		t.Fatalf("read embedded page %s: %v", path, err)
	}
	return string(raw)
}

func TestW7DOC01_EmbeddedDocsDefaultOffAndDiscontinuedCloudCompliance(t *testing.T) {
	discontinuedCloud := strings.ToLower("Gen" + "yeeCloud")
	err := fs.WalkDir(websitedocs.Pages, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content := readEmbeddedPage(t, path)
		if strings.Contains(strings.ToLower(content), discontinuedCloud) {
			t.Errorf("%s references discontinued managed cloud alias", path)
		}
		if strings.Contains(content, "默认 `https://cloud.langfuse.com`") || strings.Contains(content, "默认是 SaaS 地址") {
			t.Errorf("%s falsely claims LANGFUSE_HOST defaults to cloud.langfuse.com SaaS", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	install := readEmbeddedPage(t, "01-getting-started/02-installation.md")
	if strings.Contains(install, "`langfuse`（含 `full`）") {
		t.Error("02-installation.md falsely marks langfuse profile as included in full")
	}
}

func TestW7DOC02_EmbeddedDocsRuntimeAndSecurityContracts(t *testing.T) {
	install := readEmbeddedPage(t, "01-getting-started/02-installation.md")
	if strings.Contains(install, "env_file: [.env]") {
		t.Error("02-installation.md falsely claims app uses env_file: [.env]")
	}
	if strings.Contains(install, "minioadmin/minioadmin") || strings.Contains(install, "默认 `neo4j/password`") {
		t.Error("02-installation.md documents removed weak default credentials for MinIO/Neo4j")
	}

	config := readEmbeddedPage(t, "01-getting-started/04-configuration.md")
	if strings.Contains(config, "经 env_file 注入容器") {
		t.Error("04-configuration.md falsely claims .env is injected via env_file")
	}
	if strings.Contains(config, "从文件删除条目**不会**自动删库") {
		t.Error("04-configuration.md contradicts pruneOrphanYAMLManagedModels startup soft-deletion")
	}
	if strings.Contains(config, "minioadmin / minioadmin") || strings.Contains(config, "neo4j / password") {
		t.Error("04-configuration.md documents removed weak default credentials for MinIO/Neo4j")
	}
	for _, required := range []string{
		"GENYEE_CHROME_EXTENSION_URL", "GENYEE_CLAWHUB_SKILL_URL", "zh-TW", "builtin-genyee-assistant",
	} {
		if !strings.Contains(config, required) {
			t.Errorf("04-configuration.md missing %s", required)
		}
	}

	overview := readEmbeddedPage(t, "02-architecture/01-overview.md")
	if strings.Contains(overview, "27 家厂商") || !strings.Contains(overview, "30 家厂商") {
		t.Error("01-overview.md must document 30 built-in model providers")
	}

	retrieval := readEmbeddedPage(t, "03-features/05-retrieval-engines.md")
	if strings.Contains(retrieval, "默认 `GENYEE`") || strings.Contains(retrieval, "— / `GENYEE`") {
		t.Error("05-retrieval-engines.md documents invalid uppercase GENYEE Elasticsearch index default")
	}

	models := readEmbeddedPage(t, "03-features/06-models.md")
	if strings.Contains(models, "内置 31 个厂商") || !strings.Contains(models, "内置 30 个厂商") {
		t.Error("06-models.md must match the 30 built-in providers table")
	}

	agent := readEmbeddedPage(t, "03-features/07-agent.md")
	if !strings.Contains(agent, "builtin-genyee-assistant") {
		t.Error("07-agent.md built-in agent table omits builtin-genyee-assistant")
	}

	graph := readEmbeddedPage(t, "03-features/09-knowledge-graph.md")
	if strings.Contains(graph, "| `NEO4J_PASSWORD` | string | `password` |") {
		t.Error("09-knowledge-graph.md documents removed weak default password for NEO4J_PASSWORD")
	}

	embedDoc := readEmbeddedPage(t, "03-features/13-embed-channel.md")
	if strings.Contains(embedDoc, `HMAC-SHA256(channel.PublishToken, "{channel_id}|{session_id}")`) ||
		!strings.Contains(embedDoc, "embed-session:v2") {
		t.Error("13-embed-channel.md must document embed-session:v2 HMAC keyed by SYSTEM_SIGNING_KEY / SYSTEM_AES_KEY")
	}

	// Every <Screenshot .../> tag across all embedded docs must carry a non-empty caption="..." attribute.
	screenshotTagRE := regexp.MustCompile(`(?s)<Screenshot\b[^>]*?/>`)
	captionAttrRE := regexp.MustCompile(`\bcaption="[^"]+"`)
	err := fs.WalkDir(websitedocs.Pages, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		for _, tag := range screenshotTagRE.FindAllString(readEmbeddedPage(t, path), -1) {
			if !captionAttrRE.MatchString(tag) {
				t.Errorf("%s has <Screenshot> without non-empty caption prop: %s", path, tag)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestW7DOC03_EmbeddedAPIClientsAndDevContracts(t *testing.T) {
	apiOverview := readEmbeddedPage(t, "04-api/01-api-overview.md")
	if strings.Contains(apiOverview, "默认回环+内网段") {
		t.Error("01-api-overview.md falsely claims GENYEE_TRUSTED_PROXIES trusts private subnets by default")
	}
	if strings.Contains(apiOverview, "Bearer <JWT>") {
		t.Error("01-api-overview.md uses raw <JWT> inside Mermaid HTML label where DOMPurify strips it")
	}
	if strings.Contains(apiOverview, "Wiki index/log") {
		t.Error("01-api-overview.md still cites removed Wiki log endpoint")
	}
	for _, ev := range []string{"agent_prompt_required", "run_suspended", "work_started"} {
		if !strings.Contains(apiOverview, ev) {
			t.Errorf("01-api-overview.md SSE response_type missing %s", ev)
		}
	}

	apiModel := readEmbeddedPage(t, "04-api/02-api-model-system.md")
	if strings.Contains(apiModel, "拉取 Ollama 模型（异步任务）。权限：Admin+") ||
		strings.Contains(apiModel, "下载任务进度。权限：Viewer+") ||
		strings.Contains(apiModel, "全部下载任务列表。权限：Viewer+") {
		t.Error("02-api-model-system.md misdocuments SystemAdmin-only Ollama download/task endpoints")
	}

	apiKnowledge := readEmbeddedPage(t, "04-api/02-api-knowledge.md")
	if strings.Contains(apiKnowledge, "预览解析后的文件内容。权限：Viewer+，KB read") {
		t.Error(
			"02-api-knowledge.md misdocuments GET /knowledge/:id/preview as Viewer+ instead of Contributor+ & KB write",
		)
	}

	apiAgent := readEmbeddedPage(t, "04-api/02-api-agent-mcp.md")
	for _, route := range []string{
		"/api/v1/agent/prompts/:prompt_id", "/api/v1/agent/secure-forms/:prompt_id", "/api/v1/agent/grants",
	} {
		if !strings.Contains(apiAgent, route) {
			t.Errorf("02-api-agent-mcp.md missing %s", route)
		}
	}

	apiChannels := readEmbeddedPage(t, "04-api/02-api-channels.md")
	if !strings.Contains(apiChannels, "/prompts/:prompt_id") {
		t.Error("02-api-channels.md missing embed session prompt resolution route")
	}

	sdk := readEmbeddedPage(t, "05-clients/03-go-sdk.md")
	if strings.Contains(sdk, "loginResp.AccessToken") || !strings.Contains(sdk, "loginResp.Token") {
		t.Error("03-go-sdk.md uses non-existent loginResp.AccessToken instead of loginResp.Token")
	}

	desktop := readEmbeddedPage(t, "05-clients/05-desktop.md")
	if strings.Contains(desktop, "当前只接受 `auto`") || strings.Contains(desktop, "`ask`、`full` 尚未提供") {
		t.Error("05-desktop.md misdocuments approval_mode (ask is default and supported alongside auto)")
	}
	if !strings.Contains(desktop, "GENYEE_AUTO_UPDATE") || !strings.Contains(desktop, "package-mac-app") {
		t.Error("05-desktop.md must document default-off GENYEE_AUTO_UPDATE gating and make package-mac-app")
	}

	cliDoc := readEmbeddedPage(t, "05-clients/02-cli.md")
	if strings.Contains(cliDoc, "`genyee` / `genyee`") {
		t.Error("02-cli.md contains duplicated command name genyee / genyee")
	}

	feDoc := readEmbeddedPage(t, "05-clients/01-frontend.md")
	for _, expected := range []string{"0.9.0", "/platform/automations", "model-catalog"} {
		if !strings.Contains(feDoc, expected) {
			t.Errorf("01-frontend.md missing %s", expected)
		}
	}

	sandboxDeploy := readEmbeddedPage(t, "06-development/04-sandbox-deployment.md")
	if strings.Contains(sandboxDeploy, "`approval_mode` 当前只支持 `auto`") {
		t.Error("04-sandbox-deployment.md falsely claims approval_mode only supports auto")
	}

	dbSchema := readEmbeddedPage(t, "06-development/02-database-schema.md")
	for _, mig := range []string{"000138", "000054", "000136", "000052"} {
		if !strings.Contains(dbSchema, mig) {
			t.Errorf("02-database-schema.md missing migration %s", mig)
		}
	}
	idx36 := strings.Index(dbSchema, "| 000036 |")
	idx37 := strings.Index(dbSchema, "| 000037–000039 |")
	idx53 := strings.Index(dbSchema, "| 000053 |")
	if idx36 < 0 || idx37 < 0 || idx53 < 0 || idx36 >= idx37 || idx37 >= idx53 {
		t.Error("02-database-schema.md places SQLite 000053 out of chronological order before 000037")
	}

	extDoc := readEmbeddedPage(t, "06-development/03-extension-points.md")
	if strings.Contains(extDoc, "| 联网搜索 | `WebSearchProvider` |") || !strings.Contains(extDoc, "deepseek.go") {
		t.Error("03-extension-points.md has stale WebSearchProvider interface name or omits deepseek.go")
	}
}
