# WeKnora 上游残留标识修复规范（Residue Remediation Spec）

> **适用分支**：`chore/commercial-compliance-cleanup`（商业合规精简分支）。依据 [BRANCH_GUIDE.md](BRANCH_GUIDE.md) 第二节第 3 条第 4 项“上游品牌残留清理”，本规范内的所有改动**仅在本分支实施，不同步到 `main`**。
>
> **文档性质**：内部维护文档，已在 `.gitattributes` 中标记 `export-ignore`，并在 `scripts/export-source.sh` 中加入禁止清单，不会进入客户源码包。
>
> **版本**：v2.1（2026-11-12）。v1 按“需兼容已上线客户”编写；经项目负责人确认，**目前没有已交付或正在使用的客户**，因此 v2 **取消全部向后兼容要求**，并基于提交 `a1079cbf7` 重新扫描。v2.1 补充：缩写 `wk` 经核实为 WeKnora 简写，统一改为 `gy`（5.5）；D1 版本历史处理的推荐方案（第十一节）；D3 结论为不需要自有品牌扩展。
>
> **状态**：B0 已完成；B1 **已完成**；B2 主体已完成（字面值收尾与 B3 联动）；B3–B5 **尚未开始**。每完成一个批次，须更新第十节进度表。

---

## 一、背景与范围

### 1. 目标

项目以源码形式交付客户。交付的源码中不应再出现上游项目 WeKnora 的标识，唯一例外是法律要求保留的上游署名（见第三节）。

### 2. 关键决策：不做任何向后兼容

- 没有已上线客户 → 不存在需要迁移的客户数据、浏览器缓存、客户侧集成（Webhook 验签、网页嵌入脚本、MCP 客户端配置）。
- 因此所有标识**直接改名**，不保留旧名、不写“新旧都认”的兼容代码。
- 代码中**已经存在的**、专为兼容上游 WeKnora 旧版本而写的代码，一并**删除**（见 4.2）。
- 唯一需要单独处理的是**本机测试部署**里的数据（见第七节），这属于运维操作，不进入产品代码。

### 3. 统计口径与基线

- **范围**：客户实际拿到的源码，即 `git archive --worktree-attributes HEAD` 的导出结果（与 `export-source.sh` 一致）。`.git/`、`.agents/`、`__pycache__/`、审计文档、`test-reports/`、`docs/design` 等已被 `export-ignore` 排除，不在范围内。
- **匹配**：大小写不敏感匹配 `weknora`，同时检查文本内容、二进制文件、文件名、目录名。
- **基线（提交 `a1079cbf7`）**：

| 项目 | 数量 |
|---|---:|
| 含 `weknora` 的文本文件 | **894** |
| 文本命中次数 | **4250** |
| 含 `weknora` 的二进制文件 | 0 |
| 文件名含 `weknora` | 24 |
| 目录名含 `weknora` | 0 |
| **另：已被改名为 “GENYEE Cloud” 的 WeKnora Cloud 残留**（不含 `weknora` 字样，上表未计入） | 14 个文件 |

按目录分布（命中次数 / 文件数）：`internal` 1997 / 457，`frontend` 772 / 130，`cli` 291 / 79，`website-docs` 191 / 57，`helm` 184 / 15，`packages` 146 / 15，`mcp-server` 134 / 30，`docs`（Swagger 生成物）123 / 3，`scripts` 86 / 18，`docker` 66 / 16，`cmd` 38 / 8，`client` 27 / 15，`migrations` 20 / 13，`third_party` 18 / 8，其余为根目录配置、README、`miniprogram`、`config` 等。

### 4. 处理方式汇总

| 处理 | 对象 | 章节 |
|---|---|---|
| **保留** | 上游 MIT 许可署名及其副本；第三方 vendored 代码的许可证；BrowserSkill 第三方开源项目信息 | 第三节 |
| **删除** | WeKnora Cloud 全部功能（含“GENYEE Cloud”别名）；现有的上游兼容代码；无引用或无意义的上游素材 | 第四节 |
| **改名** | 其余全部：代码标识符、存储键、协议头、数据默认名、路径、ID、工具名、注释、文档、测试、配置、文件名 | 第五节 |
| **改名（缩写）** | WeKnora 的缩写 `wk`（约 1216 处、116 个文件、文件名 24 个）统一改为 `gy` | 5.5 |
| **重写** | 上游版本历史（CHANGELOG、发布说明）改为 GENYEE 自己的版本历史（推荐方案，待确认） | 第十一节 D1 |

---

## 二、总原则

| 编号 | 原则 |
|---|---|
| **P1 法律声明不可删** | 上游 Tencent WeKnora 以 MIT 许可发布，MIT 要求衍生作品保留原版权与许可声明。第三节列出的署名**一律保留**。这是全仓库唯一允许残留 `WeKnora` 的理由。 |
| **P2 不做兼容** | 直接改名，不保留旧名，不写兼容代码；已有的上游兼容代码删除。 |
| **P3 迁移文件可原地修改** | 由于没有已部署的客户数据库，`migrations/` 下的文件**允许直接修改**（注释与字面值）。golang-migrate 只记录版本号、不校验文件内容，修改不会导致迁移失败。注意：已执行过这些迁移的**本机数据库**不会重新执行，需按第七节处理。 |
| **P4 统一命名规则** | 按第五节第 1 条的大小写映射执行，与现有换皮代码保持一致；遇到“改名后与现有名字重名”的情况必须人工处理（第五节第 2 条），禁止盲目全局替换。 |
| **P5 测试同步修改** | 每处改名同步修改对应测试；删除的功能同步删除其测试。 |
| **P6 分批提交、每批全量验证** | 按第八节分批实施，每批单独提交，提交前完成第九节全部验证。 |
| **P7 不碰 `main`** | 本分支改名与删除会增加未来 `main → 本分支` 合并的冲突，这是已接受的代价。合并时以本分支命名为准，并对上游新增文件补做本规范的替换；WeKnora Cloud 相关改动一律保持删除。 |
| **P8 联动组件同批发布** | 后端、前端、沙箱镜像、Helm、`.env.example`、`docker-compose.yml` 之间共享的名字（路径、标签、协议头、默认值）必须在**同一批次**内一起改，保证每个提交都能独立构建、部署、运行。 |

---

## 三、保留清单（最终白名单的依据）

| 对象 | 位置 | 原因 |
|---|---|---|
| 上游 MIT 署名 | `LICENSE`，以及其副本 `frontend/LICENSE`、`website-docs/LICENSE`、`mcp-server/LICENSE.GENYEE` | P1 |
| 第三方声明 | `THIRD_PARTY_NOTICES.md`，以及副本 `frontend/`、`website-docs/`、`mcp-server/` 下的 `THIRD_PARTY_NOTICES.md`；`licenses/`；`OFFLINE_LICENSES/` | P1 |
| README 中的署名与非关联声明 | `README.md`、`README_CN.md`、`README_JA.md`、`README_KO.md` 中的 “Attribution & Non-Affiliation Notice” 段落（README 其余部分的 WeKnora 字样要改） | 署名声明，与 P1 一致 |
| 数据集许可说明中的上游署名 | `dataset/README`、`dataset/README_zh.md` 的许可段落 | 同上 |
| 第三方 vendored 代码的许可证 | `third_party/anydoc-go/LICENSE`（firecrawl/anydoc，MIT） | 第三方原始许可。注意：该目录下的 `README.md`、`Cargo.toml` 注释、`UPGRADE-0.2.4.md`、`benchmarks/` 中的 WeKnora 字样是上游团队写的说明，**不受此保护，要改名或删除** |
| BrowserSkill 第三方项目信息 | `https://github.com/Tencent/BrowserSkill`、Chrome 应用商店与 Edge 加载项的商店链接、`BrowserSkill-LICENSE` | BrowserSkill 是独立的第三方开源项目（不含 WeKnora 品牌），详见第六节；与 [REBRAND_RESERVED.md](REBRAND_RESERVED.md) 第二节一致 |

---

## 四、删除清单

### 4.1 WeKnora Cloud 功能（含“GENYEE Cloud”别名）

**决策**：2026-11-12 由项目负责人确认，WeKnora Cloud 服务已停用，从本分支彻底删除。此决策替代 BRANCH_GUIDE.md 原“保留代码、默认关闭”策略（BRANCH_GUIDE 已同步修订）。Langfuse 的 Opt-in 策略不变。

**v2 新发现**：前端与 README 中，WeKnora Cloud 已被前一轮换皮改名为 **“GENYEE Cloud”**（`GENYEECloud`、`GENYEE_CLOUD_PROVIDER`、`GENYEE_CLOUD_BASE_URL`、文案“GENYEE Cloud”），这些不含 `weknora` 字样，**必须一并删除**，否则会残留一个不存在的云服务入口。

| 层 | 内容 |
|---|---|
| 后端 - 整文件删除 | `internal/application/service/weknoracloud.go`、`internal/handler/weknoracloud.go`、`internal/infrastructure/docparser/weknoracloud_http_reader.go`、`internal/models/providers/weknoracloud.go`、`internal/models/providers/assets/weknoracloud.svg`、`internal/models/vlm/weknoracloud.go`、`internal/types/weknoracloud.go`、`internal/types/interfaces/weknoracloud.go` |
| 后端 - 共用文件（删相关分支/字段） | 模型层：`internal/models/{api/embeddings.go, api/endpoint.go, api/cohererank/client.go, api/openaicompletions/request.go, asr/asr.go, chat/chat.go, embedding/embedder.go, rerank/reranker.go, vlm/vlm.go, utils/signer.go, providers/builtin.go, providers/definition.go}`；模型目录：`internal/models/catalog/data/seed.json`，以及 `models.generated.json`（**用 `scripts/model-catalog/generate.py` 重新生成**，禁止手改）；解析引擎：`internal/infrastructure/docparser/{doc.go, engines.go, engine_registry.go}`（`WeKnoraCloudEngineName = "weknoracloud"`）；服务：`internal/application/service/{knowledge_process.go, model.go, model_catalog.go, temporary_document_parse.go, tenant.go}`；接口：`internal/handler/{initialization.go, model_catalog.go, model_credentials.go, system.go, dto/model.go}`；路由与权限：`internal/router/{router.go, routes_infra.go, rbac.go}`（`WeKnoraCloudHandler`、`RegisterWeKnoraCloudRoutes`）；类型：`internal/types/{model.go, tenant.go, tenant_api_key.go, config_redaction.go, interfaces/tenant.go}`（`WeKnoraCloudCredentials`）；容器装配：`internal/container/container.go`（`NewWeKnoraCloudService`、`NewWeKnoraCloudHandler`）；智能体：`internal/agent/tools/open_page.go` |
| 前端 | 整文件删除：`frontend/src/views/settings/WeKnoraCloudSettings.vue`、`frontend/src/utils/weknoraCloudModels.ts`（含 `GENYEE_CLOUD_PROVIDER`、`GENYEE_CLOUD_BASE_URL`）；修改：`frontend/src/views/settings/{Settings.vue, ModelSettings.vue, ParserEngineSettings.vue}`、`frontend/src/views/system/ModelCatalog.vue`、`frontend/src/components/{ModelEditorDialog.vue, credentials/CredentialResource.vue}`、`frontend/src/config/settingsAccess.ts`、`frontend/src/api/model/index.ts`；6 个语言文件中的 `weknoracloud*` 文案键与“GENYEE Cloud”文案；样式 `.engine-card--weknoracloud`、`.parser-engine-drawer--weknoracloud`；`frontend/docker-entrypoint.sh` 中的开关注入 |
| 配置与部署 | `docker-compose.yml`、`.env.example`、`.env.lite.example`、`helm/values.yaml`、`helm/templates/{app.yaml, frontend.yaml}`、`scripts/offline-bundle.sh` 中的 `GENYEE_WEKNORACLOUD_ENABLED`、`GENYEE_WEKNORACLOUD_TIMEOUT` 及注释 |
| 迁移 | `migrations/versioned/000035_add_credentials.up.sql` 注释中的 WeKnoraCloud 示例改为通用描述（P3 允许） |
| API 文档 | 删除 Go 注释后用 `swag init`（见 `Makefile` 第 264 行）**重新生成** `docs/docs.go`、`docs/swagger.json`、`docs/swagger.yaml` |
| 用户文档 | `website-docs/` 中 9 个文件的 WeKnora Cloud 章节；README 四种语言与 `CHANGELOG.md` 中的“GENYEE Cloud”描述 |
| 测试 | 删除 Cloud 专属测试（24 个测试文件涉及），修改共用测试中的 Cloud 用例 |

**数据**：本机数据库已检查，`models` 与 `tenants.credentials` 中**无** WeKnora Cloud 数据，删除后无需数据处理。`tenants.credentials` 列本身保留（还存放其他第三方凭证）。

### 4.2 现有的上游兼容代码（v2 新增，全部删除）

以下代码只为兼容“从上游 WeKnora 升级过来的旧数据/旧客户端”而存在，按 P2 删除：

| 位置 | 兼容内容 | 处理 |
|---|---|---|
| `frontend/src/composables/preferenceStorage.ts:93` | 把旧键 `WeKnora_${suffix}` 迁移到新键 | 删除迁移逻辑 |
| `frontend/src/components/Input-field.vue:1122` | `LAST_CHAT_MODEL_LEGACY_KEY = 'weknora_last_chat_model_id'` | 删除 |
| `frontend/public/weknora-widget.js` 及 `frontend/nginx.conf` 第 10、51 行的 `/weknora-widget.js` 入口 | 旧嵌入脚本名 | 删除文件与 nginx 路由，只保留 `genyee-widget.js` |
| `frontend/public/genyee-widget.js:3`（及其实现） | “同时暴露为 `window.WeKnora` 以向后兼容” | 删除 `window.WeKnora` 别名 |
| `mcp-server/weknora_mcp_server.py:906` | `GenyeeClient = WeKnoraClient` 别名 | 随第五节文件改名，类名统一，删除别名 |
| `cmd/desktop/prefs.go:16,42`、`internal/container/sandbox_host.go:182` | 旧版桌面数据目录 `WeKnora Lite` 的回退读取 | 删除回退逻辑与对应测试 |
| `internal/localsandbox/core/policy_builder.go:354` | `~/.weknora/bsk` 旧路径（第 353 行已有 `~/.genyee/bsk`） | 删除第 354 行 |
| `internal/productdocs/library.go:205,220-221` | `legacyWeKnoraDocsURL`，把上游官网链接规范化为站内路径 | 删除 |
| `frontend/src/components/SandboxConfigEditorDrawer.vue:845`、`internal/sandbox/template_catalog.go:131,156` | 识别上游沙箱镜像 `wechatopenai/weknora-sandbox` | 删除对上游镜像名的识别 |
| `internal/application/repository/retriever/milvus/migration.go:59`（`MigrateLegacyCollections`）、`repository.go:86,233` 及 `cmd/milvus-migrate/` | 迁移由旧版 WeKnora 创建的 Milvus 集合 | **实施前逐项确认**：若仅服务于“旧版本集合升级”，整体删除（含 `cmd/milvus-migrate`）；若同时承担当前版本的必要逻辑，只删除旧版本分支 |
| `packages/dsh-genyee/src/tools.ts:347` | 兼容“旧版 WeKnora 服务端缺少按名称匹配” | 简化为只支持当前服务端 |

> 实施 B1 时须再执行一次 `grep -rniE "legacy|historic|backward|compat" | grep -i weknora` 以及对 `wechatopenai` 的搜索，确认没有遗漏。

### 4.3 无引用或无意义的上游素材（删除）

| 文件 | 情况 | 处理 |
|---|---|---|
| `frontend/src/assets/img/weknora.png` | 无任何引用 | 删除 |
| `website-docs/public/videos/weknora-v0.9.0-en.srt` | 无任何引用 | 删除 |
| `website-docs/public/videos/weknora-v0.9.0-en.mp4`、`weknora-v0.9.0-poster.png` | 上游产品发布视频（2 / 4 处引用） | 删除文件，同时删除引用处的视频区块；如需视频，另行制作 GENYEE 版本 |
| `website-docs/homepage/public/docs/_home/brand/weknora-original.png` | 上游品牌原图（5 处引用） | 删除，引用处替换为 GENYEE 素材 |
| `third_party/anydoc-go/benchmarks/` | 上游团队的基准测试输出，含上游路径 | 删除（客户无需） |

---

## 五、改名清单

### 1. 命名规则（与现有换皮代码保持一致）

现有代码中已改名的标识统一使用 `GENYEE`（例如 TS 的 `searchGENYEEDocs`、`readGENYEEDoc`，Go 测试 `TestDesktopAppSupportDirUsesGENYEELiteWithLegacyFallback`）。按以下映射执行：

| 原写法 | 改为 | 示例 |
|---|---|---|
| `WeKnora`（文本、驼峰标识符） | `GENYEE` | `NewReadWeKnoraDocTool` → `NewReadGENYEEDocTool`；“WeKnora Lite” → “GENYEE Lite” |
| `weknora`（小写） | `genyee` | `weknora_embeddings` → `genyee_embeddings`；`/opt/weknora` → `/opt/genyee` |
| `WEKNORA`（大写） | `GENYEE` | `WEKNORACONFLUENCETABLE` → `GENYEECONFLUENCETABLE` |
| `Weknora`（首字母大写） | `Genyee` | `WeknoraClient` → `GenyeeClient` |
| `X-WeKnora-*` 协议头 | `X-Genyee-*` | `X-WeKnora-Signature` → `X-Genyee-Signature` |
| `com.weknora.*` Docker 标签 | `com.genyee.*` | `com.weknora.sandbox.managed` → `com.genyee.sandbox.managed` |

### 2. 改名冲突（必须人工处理）

把全部 214 个不同的 `weknora` 标识按上表映射后，有 **10 个**的目标名在代码中**已经存在**，盲目替换会产生重名或语义错误：

| 原名 | 映射后 | 已存在次数 | 处理 |
|---|---|---:|---|
| `WeKnoraCloud` | `GENYEECloud` | 48 | 随 4.1 **整体删除**，不改名 |
| `WeKnoraClient` | `GENYEEClient` | 3 | MCP Server 类名统一为一个，删除 `GenyeeClient = WeKnoraClient` 别名（4.2） |
| `WeknoraClient` | `GenyeeClient` | 1 | `packages/dsh-genyee` 客户端类与 MCP 中的别名重名，按各自包分别处理 |
| `WeKnora_` | `GENYEE_` | 22 | 前端存储键前缀，随 4.2 删除迁移逻辑后统一 |
| `WeKnora_theme` | `GENYEE_theme` | 3 | 同上 |
| `weknora_search`、`weknora_ask`、`weknora_read_document`、`weknora_list_knowledge_bases` | `genyee_*` | 8–38 | MCP 工具名：确认新名已注册后，删除旧名及其文档引用 |
| `weknora_update` | `genyee_update` | 1 | 桌面端升级临时脚本名（`website-docs/05-clients/05-desktop.md:139`），核对实现后统一 |

### 3. 改名分类明细

| 类别 | 现值（示例） | 改为 | 说明 |
|---|---|---|---|
| **数据库与存储默认名** | `DB_NAME=WeKnora`（`.env.example:158`）；SQLite `./data/weknora.db`（`internal/container/container.go:1069`、`.env.example:163`） | `genyee`；`./data/genyee.db` | |
| | 向量库：`weknora_embeddings`（Qdrant、Milvus、Doris 表前缀、腾讯云 VectorDB 集合，及 `_768`、`_1024`、`_1536` 等派生名）；`ELASTICSEARCH_INDEX=WeKnora`、`OPENSEARCH_INDEX=WeKnora`；`DORIS_DATABASE=weknora`、`TENCENT_VECTORDB_DATABASE=weknora` | `genyee_embeddings` 等 | `docker-compose.yml`、`.env.example`、`internal/container/container.go:1768` 等 |
| | 对象存储前缀 `weknora/`（S3、OBS、OSS） | `genyee/` | `docker-compose.yml`、`.env.example`、`internal/container/container.go:1450,1470,1490` |
| **内置智能体 ID** | `builtin-weknora-assistant`（`internal/types/custom_agent.go:35`、`frontend/src/api/agent/index.ts:184`、`config/builtin_agents.yaml:277`） | `builtin-genyee-assistant` | 同步修改迁移 `versioned/000121`、`000133`、`sqlite/000039`、`000050` 中的字面值（P3）；常量名 `BuiltinWeKnoraAssistantID` → `BuiltinGENYEEAssistantID` |
| **提示词 ID 与智能体工具名** | `weknora_assistant`；`search_weknora_docs`、`read_weknora_doc`；Go 侧 `ToolSearchWeKnoraDocs`、`SearchWeKnoraDocsTool` 等 | `genyee_assistant`；`search_genyee_docs`、`read_genyee_doc` | 涉及 `config/builtin_agents.yaml`、`config/prompt_templates/agent_system_prompt.yaml`、前端 `tool-results.ts`、`catalogToolDisplay.ts`、`AgentStreamDisplay.vue` |
| **HTTP 协议头** | `X-WeKnora-Signature`（嵌入渠道 Webhook）、`X-WeKnora-Desktop-Token`（桌面端） | `X-Genyee-Signature`、`X-Genyee-Desktop-Token` | 同时修改 CORS 允许头（`internal/router/router.go:365`）与迁移 `000060` 注释 |
| **浏览器存储键**（约 45 个） | `weknora_token`、`weknora_refresh_token`、`weknora_user`、`weknora_selected_tenant_id`、`WeKnora_settings`、`weknora_current_kb`、`weknora.kb.docs.viewMode`、`weknora:assistant-width`、`weknora-trace-drawer-width`、`weknora_pending_invite_token`、`weknora_sandbox_pty:` 等 | `genyee_*`、`genyee.*`、`genyee:*` | 主要在 `frontend/src/stores/settings.ts`（32 处）。小程序 `miniprogram/app.js`、`utils/config.js` 中的 `weknora_settings` 同改 |
| **前端事件名、CSS** | `weknora:open-knowledge`、`weknora:chat-file-drop`、`weknora:session-*`；`--weknora-faq-color`、`weknora-template-card*` | `genyee:*`、`--genyee-*`、`genyee-*` | |
| **嵌入脚本 / postMessage 标识** | `weknora-host`、`weknora-embed` | `genyee-host`、`genyee-embed` | 嵌入页与 `genyee-widget.js` 同批改 |
| **Redis 键 / 频道** | `weknora:sandbox:user:`、`weknora:agent_prompt:wake`、`weknora:background:shelljob:index`、`weknora:mcp_oauth_state:`、`weknora:modelsem:`、`weknora:system_settings:changed` | `genyee:*` | |
| **Docker 标签** | `com.weknora.sandbox.{managed, template, idle-ttl-seconds, skill-snapshot, skill-snapshot-source, fork-snapshot, fork-snapshot-source}` | `com.genyee.sandbox.*` | `internal/sandbox/docker_snapshot.go` 等 |
| **沙箱镜像与容器内路径** | `/weknora/tenant/skills`、`/opt/weknora/tenant/skills`、`/weknora/sessions`、`/weknora/secrets`、`/weknora/pty-prompt.sh`、`/weknora/wk-display.sh`、`/var/lib/weknora-sandbox-activity`、`/var/lib/weknora/browser/session`；模板名 `weknora-desktop`；执行器名 `weknora-exec`；测试镜像 `weknora/sandbox:*` | `/genyee/...`、`/opt/genyee/...`、`genyee-desktop` 等 | **`docker/Dockerfile.sandbox`、`docker/scripts/` 与后端必须同批修改并重建沙箱镜像**（P8）；迁移 `000086` 注释同改 |
| **本地目录与项目配置** | `~/.weknora/`、`.weknora/project.yaml`、`.weknora/requirements.json`、`.weknora/install-report.json`、`~/.weknora/jobs`、`~/.weknora/investigate/llm-stream`、`/etc/weknora/docker-certs`、`/var/lib/weknora/` | `.genyee/...` | CLI、技能安装器、本地沙箱（`internal/localsandbox/core/policy_builder.go:132,308`）、提示词（`agent_system_prompt.yaml:309`） |
| **导入占位符** | 飞书 `weknora-img://`（`internal/datasource/connector/feishu/core/markdown.go:90`）；Confluence `WEKNORACONFLUENCETABLE%dEND`（`internal/datasource/connector/confluence/markdown.go:67`） | `genyee-img://`、`GENYEECONFLUENCETABLE%dEND` | 导入转换过程中的临时标记 |
| **JSON Schema 标识** | `urn:weknora:local-browser:input`（`internal/agent/tools/browserskill_schema.go:596`） | `urn:genyee:local-browser:input` | 仅用于本地 schema 编译 |
| **PostgreSQL 会话参数** | `weknora.allow_destructive_migration`（迁移 `000043`、`000044`、`000045` 的 down 脚本与注释） | `genyee.allow_destructive_migration` | 同步修改运维文档 |
| **Helm Chart** | 模板函数 `weknora.fullname`、`weknora.labels`、`weknora.componentLabels`、`weknora.componentSelectorLabels`、`weknora.serviceName`、`weknora.secretName`、`weknora.serviceAccountName`、`weknora.formatImage`、`weknora.imagePullSecrets`、`weknora.storageClass` | `genyee.*` | `helm/templates/_helpers.tpl` 及全部模板 |
| **MCP Server** | `mcp-server/weknora_mcp_server.py`（被 30 处引用）、`WeKnoraClient` | `genyee_mcp_server.py`、`GENYEEClient` | 同步修改入口、打包配置、文档与测试 |
| **云镜像服务** | `scripts/cloud-image/systemd/weknora.service`、`weknora-firstboot.service`、`/WeKnora/.firstboot.done` | `genyee.service`、`genyee-firstboot.service` | 13 / 5 处引用同改 |
| **代码标识符、注释、日志、错误信息** | `classifyWeKnoraTemplate`、`weknoraSkillSnapshotName`、`remoteMetadataTenantID = "weknora_tenant_id"`、`"host approval mode is only available in WeKnora Lite"` 等 | 按第 1 条映射 | |
| **测试数据** | `https://weknora.example.com`、`weknora.test`、`"What is WeKnora?"`、`weknora-test-aes-key-32bytes!!!` 等 | `genyee.example.com` 等 | |
| **弱密钥黑名单** | `weknora-jwt-secret`、`weknora-system-aes-key-32bytes!!`（`internal/utils/crypto.go:18`、`presign.go:34`、`oidc_state.go:36`、`cmd/desktop/signing_key.go:77`） | 改为 `genyee-*` 占位值 | 黑名单的作用是拒绝“随示例配置公开的默认密钥”。必须保证黑名单与 `.env.example`、文档中**实际给出的占位值**一致（含已有的 `CHANGE-ME-jwt-secret`、`your-32-byte-long-encryption-key!`） |
| **上游外链** | 署名以外的 `https://github.com/Tencent/WeKnora`；`https://weknora.weixin.qq.com`（官网、`/docs/`、`/api/v1`） | GENYEE 仓库 / 官网 / 文档地址，或删除 | 优先改为读取配置项（前端 `DOCS_BASE_URL`、`WEBSITE_URL`，后端 `PRODUCT_DOCS_BASE_URL`） |
| **产品文档** | `website-docs/**`（57 个文件），含 `26-weknora-assistant.md`（11 处引用） | 品牌替换、文件改名 | **重要**：内置助手的文档检索工具检索的是编译进二进制的 `website-docs/`（`internal/productdocs/library.go:19`），这里的 WeKnora 字样会被助手直接回答给用户 |
| **CLI 文档** | `website-docs`、`README`、`CHANGELOG` 中的 `weknora auth login`、`weknora kb list` 等 | `genyee ...` | CLI 实际命令名已是 `genyee`（`cli/cmd/root.go:137`），文档与实际不符 |
| **API 文档** | `docs/docs.go`、`docs/swagger.{json,yaml}`（123 处） | — | 改 Go 注释后用 `swag init` 重新生成，禁止手改 |
| **vendored 说明文字** | `third_party/anydoc-go/README.md`、`Cargo.toml:18` 注释、`UPGRADE-0.2.4.md` | 改为 GENYEE | `LICENSE` 保留（第三节） |
| **其他配置** | `.dockerignore:30`（`WeKnora-Chrome-Extension/`）、`.air.toml`、`Makefile`、`.env.lite.example` | 改名或删除无用条目 | |

### 4. 文件改名清单（文件名含 `weknora`，共 24 个）

| 文件 | 处理 |
|---|---|
| `frontend/src/utils/weknoraCloudModels.ts`、`frontend/src/views/settings/WeKnoraCloudSettings.vue`、`internal/application/service/weknoracloud.go`、`internal/handler/weknoracloud.go`、`internal/infrastructure/docparser/weknoracloud_http_reader.go`、`internal/models/providers/assets/weknoracloud.svg`、`internal/models/providers/weknoracloud.go`、`internal/models/vlm/weknoracloud.go`、`internal/types/interfaces/weknoracloud.go`、`internal/types/weknoracloud.go` | 删除（4.1） |
| `frontend/public/weknora-widget.js`、`frontend/src/assets/img/weknora.png`、`website-docs/public/videos/weknora-v0.9.0-en.{mp4,srt}`、`weknora-v0.9.0-poster.png`、`website-docs/homepage/public/docs/_home/brand/weknora-original.png` | 删除（4.2、4.3） |
| `frontend/src/utils/weknoraEntityLinks.ts`（及 `.test.ts`）、`frontend/src/views/chat/components/tool-results/WeKnoraDocResult.vue`、`packages/dsh-genyee/test/helpers/mock-weknora.mjs`、`website-docs/03-features/26-weknora-assistant.md`、`mcp-server/weknora_mcp_server.py`、`scripts/cloud-image/systemd/weknora.service`、`weknora-firstboot.service` | `git mv` 改名，同步修改全部引用 |

### 5. 缩写 `wk` → `gy`（v2.1 新增）

**结论：`wk` 是 WeKnora 的简写，不是 wiki 的简写。** 依据：
- `docker/scripts/wk-display.sh` 文件头注释原文：“wk-display: one virtual screen ("seat") per **WeKnora** session”。
- 文档站品牌变量命名为 `--wk-logo`、`.wk-logo`、`wk-brand`、`--wk-ink`、`--wk-gold`（`website-docs/shared/brand.css`），对应的是上游品牌视觉。
- Wiki 功能相关文件中没有任何 `wk` 前缀。
- 这些命名由上游提交引入（如沙箱桌面 #4785、前端样式统一 #3376）。

按负责人决定，统一改为 `gy`（GENYEE 简写）。

**规模**：约 **1216 处、116 个文件、147 个不同标识**，另有 **24 个文件名**含 `wk`。改名后与现有名字**无冲突**（代码中目前没有任何 `gy` 前缀的标识）。

**映射规则**：

| 原写法 | 改为 | 示例 |
|---|---|---|
| `wk-` | `gy-` | `wk-display` → `gy-display`；`wk-spin` → `gy-spin`；`wk-ui-` → `gy-ui-` |
| `wk_` / `_wk_` | `gy_` / `_gy_` | `setup_wk_chrome` → `setup_gy_chrome`；`_wk_seat` → `_gy_seat` |
| `WK_` | `GY_` | `WK_SEAT` → `GY_SEAT`；`WK_SEATS_DIR` → `GY_SEATS_DIR` |
| `--wk-` / `.wk-` | `--gy-` / `.gy-` | `--wk-ink` → `--gy-ink`；`.wk-empty-state` → `.gy-empty-state` |
| 后缀 `-wk` | `-gy` | `chrome-wk.sh` → `chrome-gy.sh` |
| `wkb-`（WeKnora browser） | `gyb-` | `/tmp/wkb-chrome-` → `/tmp/gyb-chrome-` |

**排除（不得改动）**：`WKWebView`（Apple WebKit 的 API 名，与 WeKnora 无关，出现 2 处）。替换时必须使用词边界匹配，并在提交前人工检查 diff，避免误伤其他含 `wk` 字母序列的单词或二进制资源。

**分布与所属批次**：

| 区域 | 内容 | 批次 |
|---|---|---|
| 沙箱镜像脚本 `docker/scripts/`（37 个文件）、`docker/Dockerfile.sandbox` | `wk-display`、`wk-chrome`、`wk-browser-sync`、`wk-secret-fill`、`wk-auth`、`wk-ua-governor`、`wk-machine-id` 等可执行脚本，环境变量 `WK_SEAT`、`WK_SEATS_DIR`、`WK_VNC_BASE`、`WK_CGROUP_DIR`、`WK_DISPLAY*`、`WK_SYNC_*`、`WK_MACHINE_ID*`、`WK_GOVERNOR_*` 等；以及 `docker/scripts/tests/` 下的测试 | **B3**（运行时契约，沙箱镜像与后端同批，P8） |
| 后端沙箱 `internal/sandbox/`（9 个文件）、`internal/sandbox/desktopscripts/ensure.sh` | 调用 `wk-display claim/where/release`、`/usr/local/bin/wk-secret-fill`、密钥文件前缀 `wk-fill-`、`WK_SEAT` 等 | **B3** |
| 智能体提示词 `internal/agent/prompts.go:365` 及测试 | 提示词中告诉模型执行 `wk-display claim` | **B3**（与沙箱脚本名一致） |
| BrowserSkill 服务端 `internal/browserskill/focus.go:58,102` | 发给扩展的界面调用消息 ID 前缀 `wk-ui-` | **B3**（见第六节；扩展只原样回传 ID，不识别前缀，已核实） |
| 测试与文档 | `scripts/test_browserskill_extension.mjs:11`（`/tmp/wkb-chrome-`）、`internal/browserskill/daemon_security_test.go:25`（`/tmp/wkb-`）、`scripts/browserskill.md:85` | **B3** |
| 前端样式（44 个文件） | `wk-spin` 动画、`wk-popover`、`wk-empty-state` 等类名 | **B2** |
| 文档站样式与脚本 `website-docs/`（16 个文件） | `--wk-ink`、`--wk-gold`、`--wk-paper`、`wk-header`、`wk-logo`、`wk-models` 等 | **B2** |
| WeKnora Cloud 测试数据 | `internal/handler/dto/tenant_test.go` 中的 `wk-app-id-abc`、`wk-app-secret-def` | **B1**（随 Cloud 删除） |

**文件改名（24 个）**：`docker/scripts/` 下 `wk-auth.sh`、`wk-browser-sync.{sh,mjs}`、`wk-chrome.sh`、`chrome-wk.sh`、`wk-cookie-plan.mjs`、`wk-display.sh`、`wk-egress-timezone.mjs`、`wk-fingerprint-profiles.mjs`、`wk-locale-match.mjs`、`wk-machine-id.sh`、`wk-page-sync.mjs`、`wk-secret-fill.{sh,mjs}`、`wk-ua-governor.{sh,mjs}`；`docker/scripts/tests/` 下 `test_wk_auth.sh`、`test_wk_browser_sync.sh`、`test_wk_chrome.sh`、`test_chrome_wk.sh`、`test_wk_display.sh`、`test_wk_machine_id.sh`、`test_wk_secret_fill.sh`、`stubs/wk-display-stub.sh`。同步修改 `Dockerfile.sandbox` 中的 `COPY` 与安装路径（如 `/usr/local/bin/wk-*`）。

**附带修正**：`internal/browserskill/focus.go:102` 现为 `frame.ID[:6] == "wk-ui-"`（硬编码长度），改名时改用 `strings.HasPrefix(frame.ID, "gy-ui-")`。

---

## 六、BrowserSkill 浏览器连接（专项）

对应界面：工具箱 →“浏览器连接”页（安装扩展、复制配对链接、手动安装备用包）。

### 1. 结论：不需要重写浏览器插件

| 检查项 | 结果 |
|---|---|
| 扩展包 `browser-skill-weknora-0.4.1.zip`（解压后 34 个文件）中的 `weknora` 字样 | **0 处** |
| 守护进程 `bsk` 二进制中的 `weknora` 字样 | **0 处** |
| 配对与通信协议 | WebSocket 路径为 `/api/v1/local-browser/extension`，配对链接由服务端生成，**均不含 `weknora`** |
| 来源 | 独立第三方开源项目 [Tencent/BrowserSkill](https://github.com/Tencent/BrowserSkill)（MIT），由 `scripts/build_browserskill.sh` 按 `scripts/browserskill-release.json` 锁定的提交（`b8f2d4f0…`，v0.4.1）构建。产品名为 BrowserSkill，不是 WeKnora 品牌 |

**`weknora` 只出现在我方的打包命名和服务端代码里，扩展与守护进程之间的协议不受影响。** 改名只涉及我方文件，无需改动或重写扩展。

### 2. 需要改名的位置

| 位置 | 现值 | 改为 |
|---|---|---|
| `docker/Dockerfile.app:34,135-137` | 安装目录 `/opt/weknora/browserskill`；`BROWSERSKILL_BINARY`、`BROWSERSKILL_EXTENSION_PATH` 默认值 | `/opt/genyee/browserskill` |
| `scripts/build_browserskill.sh:42` | 输出文件名 `browser-skill-weknora-${version}.zip` | `browser-skill-genyee-${version}.zip` |
| `internal/browserskill/authorization.go:246` | 下载文件名 `browser-skill-weknora.zip`（界面“手动安装（备用）”） | `browser-skill-genyee.zip` |
| `frontend/src/views/settings/BrowserConnectionSettings.vue:272` | 前端下载文件名 `browser-skill-weknora.zip` | `browser-skill-genyee.zip` |
| `internal/localsandbox/core/policy_builder.go:354` | `~/.weknora/bsk` | 删除（4.2，第 353 行已有 `~/.genyee/bsk`） |
| `internal/agent/tools/browserskill_schema.go:596` | `urn:weknora:local-browser:input` | `urn:genyee:local-browser:input` |
| `internal/agent/tools/browserskill_prompt.go:23` | 注释中的 WeKnora | GENYEE |
| `internal/browserskill/focus.go:58,102`、`scripts/browserskill.md:85` | 界面调用消息 ID 前缀 `wk-ui-` | `gy-ui-`（5.5）。已核实扩展与 `bsk` 中均无 `wk-ui` 字样，扩展只原样回传 ID，前缀仅由服务端生成和识别 |
| `scripts/test_browserskill_extension.mjs:11`、`internal/browserskill/daemon_security_test.go:25` | 测试用临时目录前缀 `/tmp/wkb-chrome-`、`/tmp/wkb-` | `/tmp/gyb-chrome-`、`/tmp/gyb-`（5.5） |
| `.env.example:902,908`、`website-docs/05-clients/09-local-browser.md:62-63`、`website-docs/01-getting-started/04-configuration.md:345` | 文档中的路径与文件名 | 同上 |
| 测试：`internal/browserskill/manager_test.go:27-28,66`、`internal/handler/session/browserskill_test.go:44-58` | `weknora.example` | `genyee.example` |

上述项目必须**在同一批次内**修改（P8），否则镜像内路径与环境变量默认值不一致，会导致守护进程无法启动、扩展包无法下载。

### 3. 保持不变

- 扩展与守护进程的源代码、版本与锁定提交（`scripts/browserskill-release.json`）。
- Chrome 应用商店 / Edge 加载项链接（`BrowserConnectionSettings.vue:189-190`）与 GitHub 链接（第 29 行）：指向第三方项目，见第三节。

### 4. 功能验收（必须全部通过）

**基线已确认**：2026-11-12 在本机用当前容器内的扩展与 `bsk` 运行真实端到端测试 `TestRealExtension`，**通过（37.5 秒）**，覆盖“配对 → 打开 / 观察网页 → 标签页管理 → 暂停与人工介入 → 自动清理”。日志中的 `permission_denied`、`task_paused` 等是测试有意触发的场景。

改名后须重新执行：

```bash
# 1. 重建 app 镜像后，从容器取出新产物
docker cp GENYEE-app:/opt/genyee/browserskill/. /tmp/bsk/
mkdir -p /tmp/bsk/ext && (cd /tmp/bsk/ext && unzip -o ../browser-skill-genyee-0.4.1.zip)

# 2. 真实扩展端到端测试（需要 Chromium 与 playwright-core，可装在 /tmp，不进仓库）
#    注意：品牌版 Google Chrome 新版本已禁用 --load-extension，必须使用 Playwright 的 Chromium
BROWSERSKILL_TEST_EXTENSION=/tmp/bsk/ext \
BROWSERSKILL_TEST_BINARY=/tmp/bsk/bsk \
BROWSERSKILL_TEST_CHROMIUM=<chromium 路径> \
BROWSERSKILL_TEST_PLAYWRIGHT=<playwright-core>/index.mjs \
go test ./internal/browserskill/ -run TestRealExtension -count=1 -v
```

界面人工验收：
1. 打开“浏览器连接”页正常显示，状态为“未配对”。
2. “手动安装（备用）”下载得到 `browser-skill-genyee.zip`，可解压、可在浏览器中加载。
3. “复制配对链接”可用；在扩展的“连接设置 → 远程连接”中粘贴后配对成功，页面状态变为“已配对”。
4. 在对话中开启“本机浏览器”发送网页任务，任务能在独立窗口执行，对话中出现预览。

### 5. 不需要：GENYEE 自有品牌扩展（D3 已决定）

2026-11-12 项目负责人确认：**不需要**发布 GENYEE 自有品牌的浏览器扩展，继续使用第三方开源扩展 BrowserSkill。本规范不包含分叉、改品牌或上架商店的工作。

---

## 七、本机部署环境处理（运维操作，不进入产品代码）

本机测试部署（`docker compose` 项目 `genyee`）已执行过旧迁移，并按旧名字存有数据。B3 发布到本机时，按下表处理。**涉及数据的操作在执行前需再次征得项目负责人同意。**

| 对象 | 本机现状（2026-11-12 检查） | 处理 |
|---|---|---|
| 数据库名 | 实际库名为 `WeKnora`，本机 `.env` 中显式写了 `DB_NAME=WeKnora`（15 个知识库、353 个文档） | 二选一：① 停服务后 `ALTER DATABASE "WeKnora" RENAME TO genyee`，并把 `.env` 改为 `DB_NAME=genyee`（**保留数据，推荐**）；② 删除数据卷重建（数据全部丢失） |
| 内置智能体 ID | 1 个会话引用 `builtin-weknora-assistant` | 执行一次 `UPDATE` 改为新 ID |
| 向量库 / 对象存储 | `RETRIEVE_DRIVER=postgres`、`STORAGE_TYPE=local`，不使用 `weknora_embeddings` 集合与 `weknora/` 前缀 | 无需处理 |
| 沙箱容器与快照 | 无带 `com.weknora.sandbox.*` 标签的容器或镜像 | 无需处理 |
| Redis | 只存放短期状态 | 无需处理（重启后自动以新键重建） |
| 浏览器缓存 | 浏览器中存有 `weknora_*` 登录状态与偏好 | 首次访问新版本需重新登录，偏好恢复默认 |
| 本机 `.env` 文件 | 基于旧版上游模板，头部为“WeKnora 环境变量配置示例”，含大量已注释的 `WEKNORA_*` 变量（当前代码读取的是 `GENYEE_*`） | 以新的 `.env.example` 为模板重新生成，迁移现有的实际取值（密钥、端口等） |

---

## 八、实施批次

每个批次单独提交，提交信息注明批次号，例如 `refactor(rebrand): B2 rename identifiers and docs (WEKNORA_RESIDUE_REMEDIATION.md)`。

| 批次 | 内容 | 风险 |
|---|---|---|
| **B0** | 本规范；docreader gRPC 连接修复；`genyee-docreader` 文案修正 | 低（已完成） |
| **B1** | 删除 WeKnora Cloud（4.1，含“GENYEE Cloud”别名及其测试数据）；删除现有上游兼容代码（4.2） | 中 |
| **B2** | 不影响运行时契约的改名：注释、日志、错误信息、测试数据、内部代码标识符、产品文档与 CLI 文档、README（署名段落除外）、vendored 说明文字、Swagger 重新生成；前端与文档站样式中的 `wk` → `gy`（5.5） | 低（量大） |
| **B3** | 运行时契约改名（必须一次性完成，P8）：数据库与存储默认名、智能体 ID 与工具名、协议头、浏览器存储键、前端事件与 postMessage 标识、Redis 键、Docker 标签、沙箱镜像与路径、本地目录、导入占位符、Helm、MCP Server、云镜像服务、**BrowserSkill 打包路径与 `wk-ui-` 前缀（第六节）**、**沙箱脚本与环境变量的 `wk` → `gy`（5.5）**、迁移文件字面值 | **高** |
| **B4** | 文件改名与素材清理（4.3、5.4）、上游外链、版本历史重写（D1，确认后执行） | 中 |
| **B5** | 本机环境处理（第七节，需负责人确认）；重建并部署全部镜像；BrowserSkill 功能验收；最终审计（导出包扫描只剩白名单） | — |

---

## 九、验收标准

### 1. 每个批次提交前必须全部通过

```bash
# 后端
go build ./... && go vet ./... && go test ./...

# 前端（必须带内存参数，否则 vite 构建会内存不足）
cd frontend
NODE_OPTIONS="--max-old-space-size=4096" npx vue-tsc --build
npx tsx --test $(find src -name "*.test.ts" -o -name "*.test.mjs")
npm run check-i18n
npm run build

# 涉及时执行
cd mcp-server && python -m pytest
cd docreader && python -m pytest
cd packages/dsh-genyee && npm test
node --test tests/miniprogram/miniprogram.test.js
helm lint helm/ && helm template helm/ > /dev/null
```

### 2. 残留扫描（在导出包上执行）

```bash
rm -rf /tmp/genyee-export && mkdir -p /tmp/genyee-export
git archive --worktree-attributes HEAD | tar -x -C /tmp/genyee-export
cd /tmp/genyee-export
# 文本内容（已排除许可证文件；输出应只剩 README 署名段落与 dataset 许可段落，逐条人工确认）
grep -rnI -i 'weknora' . | grep -vE '^\./(LICENSE|THIRD_PARTY_NOTICES\.md|licenses/|OFFLINE_LICENSES/)|/(LICENSE|LICENSE\.GENYEE|THIRD_PARTY_NOTICES\.md):|^\./third_party/anydoc-go/LICENSE'
# 文件名与目录名
find . -iname '*weknora*'
# 二进制文件
grep -rlai 'weknora' . | xargs -r file | grep -v text
# “GENYEE Cloud” 别名残留
grep -rniE 'genyee[ _-]?cloud' .
# 缩写 wk 残留（排除 Apple 的 WKWebView）
grep -rnIE '\bwk[-_]|[-_]wk\b|[-_]wk[-_]|\bWK_[A-Z0-9_]+|\bwkb[-_]|--wk-|\.wk-' . | grep -v 'WKWebView'
find . -regextype posix-extended -iregex '.*(^|[/_-])wk([_.-]|$).*'
```

### 3. 最终白名单（B5 完成后，扫描结果只允许出现以下内容）

| 允许保留 | 原因 |
|---|---|
| `LICENSE`、`frontend/LICENSE`、`website-docs/LICENSE`、`mcp-server/LICENSE.GENYEE` 中的上游署名 | P1 |
| 各处 `THIRD_PARTY_NOTICES.md`、`licenses/`、`OFFLINE_LICENSES/` 中的上游声明 | P1 |
| 四种语言 README 的 “Attribution & Non-Affiliation Notice” 段落 | 署名声明 |
| `dataset/README`、`dataset/README_zh.md` 许可段落中的上游署名 | 署名声明 |
| `third_party/anydoc-go/LICENSE` | 第三方原始许可 |

除上表外，**不允许任何 `weknora` 字样、文件名、目录名、“GENYEE Cloud”残留，以及 WeKnora 缩写 `wk`（`WKWebView` 除外）**。不再有任何“兼容期”例外。

### 4. 功能回归（B3、B5 必做）

- 第六节第 4 条 BrowserSkill 端到端测试与界面验收。
- 全新环境（空数据卷）`docker compose up` 后：注册登录、创建知识库、上传并解析文档（docreader 正常、设置页显示 docreader 版本号）、检索问答、内置助手对话与文档检索工具、嵌入渠道（`genyee-widget.js`）与 Webhook 签名头、沙箱执行、MCP Server、CLI（`genyee auth login`、`genyee kb list`）。
- `helm template` 渲染结果中资源名与标签为 `genyee` 前缀。

---

## 十、进度

| 批次 | 状态 | 提交 | 备注 |
|---|---|---|---|
| B0 | 已完成 | `a1079cbf7` | 规范 v1 + docreader gRPC 连接修复 + `genyee-docreader` 文案修正 + BRANCH_GUIDE 决策更新 |
| — | 已完成 | `f40d3b9a3` | 本文档 v2：取消兼容要求；重新扫描；补充“GENYEE Cloud”别名、现有兼容代码、改名冲突、BrowserSkill 专项、本机环境处理 |
| — | 已完成 | 本文档 v2.1 提交 | 核实 `wk` 为 WeKnora 简写并制定 `wk` → `gy` 规则（5.5）；D1 推荐方案；D3 决定不做 |
| B1 | 已完成 | `929cc59d6` | 已删除停用云服务（后端、前端、目录、部署开关、API/用户文档和专属测试）、旧品牌兼容入口、旧 Milvus 迁移与运行时旧 schema 分支。保留泛用附加模型凭证（LKEAP / 火山重排仍使用）。Go build/vet/test、CLI build/vet/test、前端类型/国际化/构建与 2719 项测试、浏览器交接测试、MCP 151 项测试（含 74 子测试）、DSH 60 项测试、脚本/Helm 26 项测试与 34 项合规检查均通过。D1 历史条目按负责人决定暂时保留。 |
| B2 | 主体完成，字面值待 B3 联动 | 本批提交（B2） | 已改 Go 内部标识符、源码注释、文档正文与前端/文档站 `wk` 样式；诊断及测试字面值随 B3 契约统一替换。Go build/vet/test、CLI build/vet/test、前端类型/国际化/构建与 2719 项测试（零跳过）、官网/文档构建及链接/60 个 Mermaid/83 页站点检查通过。未改动许可声明与二进制资源；保留历史条目，仅修复历史说明中的 CHANGELOG 死链。 |
| B3 | 未开始 | | |
| B4 | 未开始 | | |
| B5 | 未开始 | | |

---

## 十一、决策记录

v1 中关于“是否已有客户”“持久化默认名是否兼容”“智能体 ID 是否改”“签名头兼容多久”的问题已全部关闭（不兼容、直接改）。

| 编号 | 问题 | 结论 | 状态 |
|---|---|---|---|
| D1 | 上游版本历史怎么处理？ | 负责人于 2026-11-13 确认暂时保留，先处理其余整改；不执行下述重写建议 | **暂缓** |
| D2 | 缩写 `wk` 是否处理？ | 经核实 `wk` 是 WeKnora 的简写（不是 wiki），统一改为 `gy`，规则见 5.5 | **已决定**（2026-11-12） |
| D3 | 是否发布 GENYEE 自有品牌的浏览器扩展？ | 不需要，继续使用第三方 BrowserSkill | **已决定**（2026-11-12） |

### D1 推荐方案：改为 GENYEE 自己的版本历史，删除上游历史条目

**涉及内容**

| 位置 | 现状 |
|---|---|
| `CHANGELOG.md` | 1844 行，上游 v0.2.0 – v0.9.0 的完整历史；112 处上游 PR 编号（如 `#4302`）；比较链接 `https://github.com/Usir233/GENYEE/compare/v0.8.3...v0.9.0` |
| `cli/CHANGELOG.md` | 575 行，65 处上游引用 |
| `mcp-server/CHANGELOG.md` | 151 行，6 处上游引用 |
| `website-docs/07-releases/` | `v0.8.0.md`、`v0.8.2.md`、`v0.8.3.md`、`v0.9.0.md` 四篇上游发布说明 |
| 四种语言 README | 末尾的上游历史版本列表（v0.2.0 – v0.7.2） |

**为什么不建议“保留历史、只做品牌替换”**

1. **会变成不真实的记录**：历史条目描述的是上游团队在 GENYEE 存在之前做的事，改成 GENYEE 名义等于宣称这些工作由 GENYEE 完成、发生在那些日期。
2. **链接全部失效**：`origin` 仓库目前**没有任何 tag**，`compare/v0.8.3...v0.9.0` 这类链接打不开；112 处 PR 编号在 GENYEE 仓库中对应的是不存在或无关的 PR。
3. **无法律要求**：MIT 只要求保留版权与许可声明（第三节已保留），不要求保留变更日志。
4. **维护者不丢信息**：上游完整历史仍保存在 git 历史和 `upstream` 分支中（这两者都不交付客户）。

**具体做法（B4 执行）**

1. `CHANGELOG.md`：清空上游条目，只保留一个以当前 `VERSION`（`0.9.0`）命名的 GENYEE 首个版本条目，按功能模块概述现有能力（知识库、Wiki、智能体、沙箱、IM 集成、嵌入渠道、浏览器连接、MCP 等），不写 PR 编号与比较链接，也不提及上游。今后从这个版本开始按 GENYEE 自己的发布记录追加。
2. `cli/CHANGELOG.md`、`mcp-server/CHANGELOG.md`：同样处理，各保留一个首版条目。
3. `website-docs/07-releases/`：删除四篇上游发布说明，新建一篇 GENYEE 首版发布说明（如 `v0.9.0.md`，内容与 `CHANGELOG.md` 首版条目一致）。注意以下依赖须同步修改，否则构建失败或出现死链：
   - `website-docs/embed.go:11` 的 `//go:embed ... 07-releases/*.md` 要求该目录**至少保留一个 `.md` 文件**，否则 Go 编译报错；
   - `website-docs/.vitepress/theme/SiteHeader.vue:15` 的“版本”菜单链接指向 `/docs/07-releases/v0.8.3`，需改为新页面；
   - `website-docs/scripts/check-site.mjs:70` 的检查路径列表包含四个旧版本页面，需更新；
   - `internal/productdocs/library.go:31` 的“版本发布”分类保留，内置助手会检索这篇新的发布说明。
4. 四种语言 README：删除上游历史版本列表，改为指向 `CHANGELOG.md`。
5. 版本号本身（`VERSION` 文件为 `0.9.0`）是否从 GENYEE 的角度重新编号（例如 `1.0.0`），不在本规范范围内，由负责人另行决定；本方案沿用现有版本号。

---

## 附录：调研中确认的事实

- 扫描基线：提交 `a1079cbf7`，导出包 894 个文本文件、4250 处，二进制 0 个，文件名 24 个，目录名 0 个。
- 已有换皮代码的命名惯例是 `GENYEE`（全大写），例如 `searchGENYEEDocs`、`readGENYEEDoc`、`GENYEECloud`。
- WeKnora Cloud 在前端被改名为“GENYEE Cloud”，涉及 14 个文件：6 个语言文件、`ModelEditorDialog.vue`、`weknoraCloudModels.ts`、`WeKnoraCloudSettings.vue`、4 种语言 README、`CHANGELOG.md`。
- 代码读取的环境变量已全部为 `GENYEE_*`；导出包中仅剩 `GENYEE_WEKNORACLOUD_ENABLED`、`GENYEE_WEKNORACLOUD_TIMEOUT`（随 4.1 删除）。
- `WEKNORACONFLUENCETABLE` 不是环境变量，而是 Confluence 导入时的文本内临时占位符。
- 内置助手的文档检索工具不联网，检索编译进二进制的 `website-docs/`（`internal/productdocs/library.go:19`）。
- CLI 实际命令名已是 `genyee`（`cli/cmd/root.go:137`），文档中仍有 `weknora ...` 示例。
- BrowserSkill 扩展包与 `bsk` 守护进程中均无 `weknora`、`wk-ui`、`wk-` 字样；`TestRealExtension` 基线通过（2026-11-12，37.5 秒）。
- `wk` 为 WeKnora 简写：`docker/scripts/wk-display.sh` 头注释写明 “per WeKnora session”；文档站品牌变量为 `--wk-logo` 等；Wiki 相关文件中没有 `wk` 前缀。代码中目前没有任何 `gy` 前缀，改名无冲突。
- `origin` 仓库目前没有任何 git tag。
- `frontend/nginx.conf` 同时提供 `/genyee-widget.js` 与 `/weknora-widget.js`（第 45、51 行）。
- 本机数据库实际名为 `WeKnora`，本机 `.env` 中 `DB_NAME=WeKnora`；`models` 与 `tenants.credentials` 中无 WeKnora Cloud 数据；1 个会话引用 `builtin-weknora-assistant`。
- `golang-migrate` 只记录已执行的版本号，不校验迁移文件内容。
