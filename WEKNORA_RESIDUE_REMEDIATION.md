# WeKnora 上游残留标识修复规范（Residue Remediation Spec）

> **适用分支**：`chore/commercial-compliance-cleanup`（商业合规精简分支）。依据 [BRANCH_GUIDE.md](BRANCH_GUIDE.md) 第二节第 3 条第 4 项“上游品牌残留清理”，本规范内的所有改动**仅在本分支实施，不同步到 `main`**。
>
> **文档性质**：内部维护文档，已在 `.gitattributes` 中标记 `export-ignore`，不会进入 `./scripts/export-source.sh` 导出的客户源码包。
>
> **状态**：规范已制定（B0 完成），B1–B6 **尚未开始实施**。统计基线为提交 `8b2ee40a7`。每完成一个批次，须更新第七节进度表。

---

## 一、背景与范围

### 1. 为什么需要本规范

此前的换皮工作只处理了**用户在界面上能看到**的品牌词。但本项目会**以源码形式交付给客户**，客户能看到全部代码、注释、配置与文档。因此源码中残留的 `WeKnora` / `weknora` 标识同样需要清理。

但这些残留里有相当一部分**不是普通文本**，而是：已经写进数据库/浏览器/Redis/对象存储的数据键名、对外协议（Webhook 签名头、嵌入脚本、MCP 入口）、法律声明、安全黑名单。**对这些做“全局查找替换”会造成数据丢失、用户被登出、客户集成中断，甚至安全漏洞。** 本规范的目的就是把每一类残留的处理方式事先定清楚。

### 2. 统计口径

- **只统计客户实际拿到的源码**：即 `git archive --worktree-attributes HEAD` 的导出结果（与 `export-source.sh` 一致）。`.git/`、`.agents/`、`__pycache__/`、审计文档、`test-reports/`、`docs/design` 等已被 `export-ignore` 排除，不在本规范范围内。
- **匹配规则**：大小写不敏感匹配 `weknora`。
- **基线（提交 `8b2ee40a7`）**：**893 个文件、4255 处**；另有 **24 个文件的文件名**包含 `weknora`。

### 3. 分类统计（启发式分类，每行只计入第一个命中的类别）

| 类别 | 文件数 | 命中数 | 处理规则（见第三节） |
|---|---:|---:|---|
| A. 许可 / 版权 / 第三方声明 | 10 | 90 | **R0 保留** |
| B. WeKnora Cloud 功能 | 106 | 723 | **R1 删除**（见第四节） |
| C. 上游仓库 / 官网外链 | 27 | 54 | R5 |
| D. 数据库迁移文件 | 12 | 18 | **R0 保留** |
| E. 浏览器存储键 | 53 | 184 | R3 |
| F. 前端自定义事件 / postMessage 协议 | 31 | 76 | R2 / R3 |
| G. CSS 变量 `--weknora-*` | 4 | 7 | R2 |
| H. 环境变量名 | 2 | 2 | R1 / R2 |
| I. 持久化数据默认名（含沙箱路径、本地目录） | 141 | 509 | R3 / R4 |
| J. HTTP 头 / 协议标识 | 13 | 14 | R3 |
| K. URL 路径 `/weknora/...` | 108 | 250 | R2 / R3 |
| L. 本地目录 / 配置路径 | 36 | 73 | R3 |
| M. 代码标识符 | 137 | 627 | R2（个别 R3） |
| N. 注释 / 文档 / 日志 / 测试文本 | 502 | 1628 | R2 |

> 分类由脚本按正则自动归类，存在交叉，数字用于评估规模，**不作为验收依据**。验收以第六节的“最终白名单扫描”为准。

---

## 二、总原则（必须遵守）

| 编号 | 原则 |
|---|---|
| **P1 法律声明不可删** | 上游 Tencent WeKnora 以 MIT 许可发布，MIT 要求衍生作品保留原版权与许可声明。`LICENSE`、`THIRD_PARTY_NOTICES.md`、`licenses/`、`OFFLINE_LICENSES/` 中的 WeKnora 署名**一律保留**。 |
| **P2 已执行的迁移不可改** | `migrations/` 下已发布的迁移文件（含注释）**禁止修改**，否则 golang-migrate 校验和升级路径会出问题。需要变更数据时，**新增**迁移文件。 |
| **P3 持久化 / 对外契约必须兼容** | 凡是已经写入外部（数据库、浏览器存储、Redis、对象存储、向量库、Docker 标签、客户系统）的标识，改名时必须做到“**读：新旧都认；写：只写新的**”，并保留至少一个大版本的兼容期。 |
| **P4 安全黑名单只增不减** | 代码里用来**拒绝弱默认密钥**的占位值（如 `weknora-jwt-secret`、`weknora-system-aes-key-32bytes!!`）必须**保留**，因为老部署可能仍在使用这些默认值。只能**新增** `genyee-*` 占位值，不能删除旧值。 |
| **P5 测试同步修改** | 每处改名必须同步修改对应测试；新增兼容逻辑必须有“旧值仍可读”的测试。 |
| **P6 分批提交、每批全量验证** | 按第五节分批实施。每批单独提交，提交前完成第六节全部验证。禁止一次性全局替换。 |
| **P7 不碰 `main`** | 本分支改名会增加未来 `main → 本分支` 合并时的冲突，这是已接受的代价。合并时以本分支命名为准，并对上游新增文件补做本规范的替换。 |

---

## 三、处理规则详解

### R0 保留（不改）

| 对象 | 位置 | 原因 |
|---|---|---|
| 上游 MIT 署名 | `LICENSE`、`THIRD_PARTY_NOTICES.md`、`licenses/`、`OFFLINE_LICENSES/` | P1 法律要求 |
| 迁移文件中的所有 WeKnora 文本 | `migrations/versioned/*`、`migrations/sqlite/*`（共 12 个文件） | P2。包括注释、`'builtin-weknora-assistant'` 字面值，以及 PostgreSQL 会话参数 `weknora.allow_destructive_migration`（运维手册里已写明此参数名，改名需新迁移 + 文档同步，收益低，**保留**） |
| 弱密钥黑名单 | `internal/utils/crypto.go:18`、`internal/utils/presign.go:34`、`internal/utils/oidc_state.go:36`、`cmd/desktop/signing_key.go:77` | P4。可在旁边新增 `genyee-*` 占位值 |
| 兼容读取旧值的代码 | 实施 R3 后新增的 legacy 常量 | 兼容期内保留，命名统一为 `legacy*` 并注释“兼容上游 WeKnora 旧值” |
| 第三方 vendored 代码 | `third_party/anydoc-go/`（README、benchmarks、Cargo.toml） | 第三方原样代码，按其自身许可保留原文 |

### R1 删除

仅适用于 **WeKnora Cloud** 功能，详见第四节。

### R2 直接替换（无兼容负担）

只在本进程/本仓库内部使用、不落盘、不跨版本的标识，直接替换为 GENYEE 命名：

| 对象 | 示例 | 替换规则 |
|---|---|---|
| 注释、日志、错误信息 | `// uploaded to WeKnora often...`、`"host approval mode is only available in WeKnora Lite"` | `WeKnora` → `GENYEE`；`WeKnora Lite` → `GENYEE Lite` |
| 测试数据和测试名 | `"What is WeKnora?"`、`weknora_test`、`"weknora:test"` | 统一换成 `genyee` |
| 示例域名 | `https://weknora.example.com`、`weknora.test` | → `genyee.example.com`、`genyee.test` |
| Go / TS / Python 内部标识符 | `classifyWeKnoraTemplate`、`NewReadWeKnoraDocTool`、`searchWeKnoraDocsSchema` | `WeKnora` → `Genyee`（Go 导出名首字母大写），`weknora` → `genyee` |
| CSS 类名与变量 | `weknora-template-card*`、`--weknora-faq-color` | → `genyee-*`，同时替换所有引用处 |
| 同页面内的前端自定义事件 | `weknora:open-knowledge`、`weknora:chat-file-drop`、`weknora:session-*` 等 | → `genyee:*`。仅在单个页面运行时内派发和监听，前后端同版本发布，无兼容负担 |
| Helm 模板内部 define 名 | `weknora.fullname`、`weknora.labels`、`weknora.componentLabels` 等 | → `genyee.*`。仅在 chart 内部互相引用 |
| Swagger / OpenAPI 描述 | `docs/docs.go`、`docs/swagger.{json,yaml}` | 先改 Go 注释，再**用 swag 重新生成**，禁止手改生成文件 |
| 文档站与 CHANGELOG | `website-docs/**`、`CHANGELOG.md`、`cli/CHANGELOG.md` | 品牌词替换；CLI 示例中的 `weknora kb list` 等应改为实际命令名 `genyee`（`cli/cmd/root.go` 已是 `Use: "genyee"`，文档与实际不符） |
| 文件名 | 见本节末“文件改名清单” | `git mv`，同时修改所有 import / 引用 |

### R3 替换 + 兼容迁移（P3 适用）

这些标识已经落到外部存储或被外部系统使用，**必须带兼容逻辑**。

| 对象 | 现值（示例） | 落在哪里 | 兼容方案 |
|---|---|---|---|
| **浏览器存储键**（约 45 个） | `weknora_token`、`weknora_refresh_token`、`weknora_user`、`weknora_selected_tenant_id`、`WeKnora_settings`、`weknora_last_chat_model_id`、`weknora_current_kb`、`weknora.kb.docs.viewMode`、`weknora:assistant-width`、`weknora-trace-drawer-width`、`weknora_pending_invite_token` 等 | 用户浏览器 localStorage / sessionStorage | 新增统一的存储迁移模块，在应用启动、读取任何键之前执行一次：遍历所有 `weknora` / `WeKnora` 前缀的键，**新键不存在时**复制旧值到 `genyee` 前缀的新键，然后删除旧键。全部读写代码改用新键。**不做迁移会导致所有在线用户被登出、偏好丢失。** |
| **Redis 键 / 频道** | `weknora:sandbox:user:`、`weknora:agent_prompt:wake`、`weknora:background:shelljob:index`、`weknora:mcp_oauth_state:`、`weknora:modelsem:`、`weknora:system_settings:changed` | Redis | 多数为短期状态，可直接切换，但须：①多副本部署时**所有副本同版本升级**（发布/订阅频道两端必须一致）；②`sandbox:user` 绑定与 `shelljob:index` 升级时会失效一次，需在发布说明中注明，或在启动时把旧前缀键 `RENAME` 成新前缀。 |
| **Docker 标签** | `com.weknora.sandbox.managed`、`.template`、`.idle-ttl-seconds`、`.skill-snapshot*`、`.fork-snapshot*` | 宿主机上已存在的沙箱容器与快照镜像 | 新容器写 `com.genyee.sandbox.*`；**回收、清理、快照查找必须同时识别新旧两种标签**，否则升级后旧沙箱容器和快照会变成无人管理的“孤儿”，持续占用资源。 |
| **沙箱容器内路径** | `/weknora/tenant/skills`、`/opt/weknora/tenant/skills`、`/weknora/sessions`、`/weknora/secrets`、`/weknora/pty-prompt.sh`、`/weknora/wk-display.sh`、`/var/lib/weknora-sandbox-activity`、`/var/lib/weknora/browser/session` | 沙箱镜像（`docker/Dockerfile.sandbox`）与后端约定的挂载路径 | **后端与沙箱镜像必须同批改、同批发布**。后端探测路径时兼容旧路径，以支持仍在运行旧沙箱镜像的环境。注意：`migrations/versioned/000086` 的注释里写了 `/opt/weknora/...`，按 P2 保留。 |
| **本地目录 / 配置文件** | `~/.weknora/`、项目内 `.weknora/project.yaml`、`.weknora/requirements.json`、`.weknora/install-report.json`、`/opt/weknora/`、`/var/lib/weknora/` | 客户机器、CLI 用户、技能目录 | 新路径为 `.genyee/`。读取时**新路径优先，找不到再读旧路径**；写入只写新路径。CLI 可在首次运行时提示或自动迁移。`cmd/desktop/prefs.go` 与 `internal/container/sandbox_host.go` 已有 `legacyAppSupportName = "WeKnora Lite"` 兼容逻辑，沿用这种写法。 |
| **内置智能体 ID** | `builtin-weknora-assistant`（`internal/types/custom_agent.go:35`、`frontend/src/api/agent/index.ts:184`、`config/builtin_agents.yaml:277`） | 数据库：会话 `agent_config.agent_id`、`automations.agent_id` 等 | **选项 1（推荐）保持 ID 不变**，只改常量名（如 `BuiltinGenyeeAssistantID`）和注释。ID 是内部主键，用户看不到，改它需要数据迁移，风险高、收益低。选项 2：新增迁移把所有引用更新为 `builtin-genyee-assistant`，并在代码中把旧 ID 映射为新 ID。**需决策，见第八节。** |
| **智能体工具名** | `search_weknora_docs`、`read_weknora_doc`；提示词 ID `weknora_assistant` | 数据库中的智能体配置（允许的工具列表）、历史会话的工具调用记录、前端工具结果渲染 | 新名为 `search_genyee_docs` / `read_genyee_doc`。工具注册时**旧名作为别名**保留；前端渲染同时识别新旧名（否则历史会话里的工具结果无法正常显示）。 |
| **Webhook 签名头** | `X-WeKnora-Signature`（嵌入渠道 Webhook，`migrations/000060` 注释亦有提及） | **客户自己的 Webhook 接收服务**在校验这个头 | 兼容期内**同时发送** `X-Genyee-Signature` 与 `X-WeKnora-Signature`（值相同），在文档和发布说明中标注旧头将废弃，至少保留一个大版本后再移除。 |
| **桌面端令牌头** | `X-WeKnora-Desktop-Token` | 桌面客户端 ↔ 本地服务 | 服务端同时接受新旧两个头；客户端只发新头。 |
| **嵌入脚本与 postMessage 协议** | `frontend/public/weknora-widget.js`；消息来源标识 `weknora-host`、`weknora-embed` | **客户网站**通过 `<script src=".../weknora-widget.js">` 嵌入，并与 iframe 互发消息 | 保留 `weknora-widget.js` 作为兼容文件（内容与 `genyee-widget.js` 一致，或由 nginx 指向同一文件，当前 `frontend/nginx.conf` 已有相关配置）；iframe 内**同时接受新旧两种消息标识**；文档只介绍 `genyee-widget.js`。 |
| **MCP Server 入口** | `mcp-server/weknora_mcp_server.py`（被 30 处引用） | 客户的 MCP 客户端配置里写的是这个模块名或脚本路径 | 主实现改名为 `genyee_mcp_server.py`；保留 `weknora_mcp_server.py` 作为只做 re-export 的兼容文件，并打印废弃提示。 |
| **导入占位符** | 飞书 `weknora-img://`（`internal/datasource/connector/feishu/core/markdown.go:90`）；Confluence `WEKNORACONFLUENCETABLE%dEND`（`internal/datasource/connector/confluence/markdown.go:67`） | 导入转换过程中的文本内临时标记，正常情况下转换结束前会被替换掉 | 改为 `genyee-img://`、`GENYEECONFLUENCETABLE%dEND`。实施前须确认是否有残留落库（查 `knowledges` / `chunks` 内容），如有，解析时同时识别旧前缀。 |
| **云镜像 systemd 服务名** | `scripts/cloud-image/systemd/weknora.service`、`weknora-firstboot.service`、`/WeKnora/.firstboot.done` | 已售出的云主机镜像 | 新镜像使用 `genyee.service`；如需支持就地升级，安装脚本要先停用并移除旧服务。 |
| **前端同名但跨页面持久的事件键** | `weknora:new-user-guide-done:v1` 等实际存在 localStorage 中的值 | 浏览器存储 | 归入“浏览器存储键”统一迁移 |

### R4 持久化数据默认名（**需决策**，默认“老环境锁定旧值”）

这些默认值决定了数据**存在哪里**。直接改默认值，**已部署环境升级后会“找不到”原来的全部数据**（知识库检索为空、文件 404）。

| 对象 | 现默认值 | 涉及位置 |
|---|---|---|
| 向量集合名 / 表前缀 | `weknora_embeddings`（及 `_768`、`_1024`、`_1536`、`_multilingual` 等派生名） | `docker-compose.yml`（Qdrant、Milvus）、`.env.example`（Qdrant、Milvus、Doris、腾讯云 VectorDB）、`internal/application/**`、`cmd/milvus-migrate` |
| 对象存储路径前缀 | `weknora/` | `docker-compose.yml`、`.env.example`（S3、OBS、OSS） |
| BrowserSkill 二进制与扩展包路径 | `/opt/weknora/browserskill/...`、`browser-skill-weknora-0.4.1.zip` | `.env.example`；扩展包文件名来自第三方，见 REBRAND_RESERVED.md 第二节 |

**规则**：
1. **新部署**：默认值改为 `genyee_embeddings`、`genyee/`。
2. **老环境升级**：在升级文档中**醒目提示**，必须在 `.env` 中显式写回旧值（`QDRANT_COLLECTION=weknora_embeddings`、`S3_PATH_PREFIX=weknora/` 等）；或使用 `cmd/milvus-migrate` 等工具迁移数据后再切换。
3. 启动时如检测到“新默认值下没有数据、但旧默认值下有数据”，应打印 WARN 日志提示配置问题（可选增强）。
4. **如果确认目前没有任何已上线的客户环境**，可简化为直接改默认值，不做兼容。见第八节。

### R5 外部链接与功能性外部依赖

| 对象 | 示例 | 处理 |
|---|---|---|
| 上游仓库链接 | `https://github.com/Tencent/WeKnora`（12 处） | 法律声明中的保留（P1）；其余（README、文档、代码注释、`internal/infrastructure/docparser` 引用等）替换为 GENYEE 仓库/官网，或删除 |
| 上游官网与文档站 | `https://weknora.weixin.qq.com`、`/docs/...`、`/api/v1` | 替换为 GENYEE 官网 / 文档地址；优先改为读取已有配置项（前端 `DOCS_BASE_URL`、`WEBSITE_URL`，后端 `PRODUCT_DOCS_BASE_URL`） |
| **文档检索工具的内容来源** | `search_weknora_docs` / `read_weknora_doc`（`internal/productdocs/`） | 该工具**不联网**，检索的是编译进二进制的 `website-docs/` 内容（`internal/productdocs/library.go:19`）。因此 `website-docs/` 中残留的 WeKnora 文字会被内置助手**原样回答给用户**，B2 清理 `website-docs/` 时须一并覆盖。`legacyWeKnoraDocsURL`（`library.go:205`）仅用于把旧链接规范化为站内路径，属于兼容代码，按 R3 改名为 `legacy*` 常量后保留。工具名本身按 R3 处理。 |
| 品牌图片与视频 | `frontend/src/assets/img/weknora.png`、`website-docs/homepage/public/docs/_home/brand/weknora-original.png`、`website-docs/public/videos/weknora-v0.9.0-*`（mp4/srt/png） | 未被引用的直接删除；仍被引用的替换为 GENYEE 素材或删除对应页面 |
| 内置助手与提示词 | `config/builtin_agents.yaml`、`config/prompt_templates/agent_system_prompt.yaml`（`weknora_assistant`） | 提示词正文中的产品名会被大模型复述给用户，属于**用户可见**，按 R2 替换；提示词 ID 按 R3（与工具名一同兼容） |
| 环境变量 | `GENYEE_WEKNORACLOUD_ENABLED`、`GENYEE_WEKNORACLOUD_TIMEOUT` | 随 R1 删除。（扫描中另有 `WEKNORACONFLUENCETABLE`，它**不是环境变量**，是 Confluence 导入时的文本内临时占位符，见 R3“导入占位符”一行） |

### 文件改名清单（文件名含 `weknora`，共 24 个）

| 文件 | 处理 |
|---|---|
| `frontend/src/utils/weknoraCloudModels.ts`、`frontend/src/views/settings/WeKnoraCloudSettings.vue`、`internal/application/service/weknoracloud.go`、`internal/handler/weknoracloud.go`、`internal/infrastructure/docparser/weknoracloud_http_reader.go`、`internal/models/providers/assets/weknoracloud.svg`、`internal/models/providers/weknoracloud.go`、`internal/models/vlm/weknoracloud.go`、`internal/types/interfaces/weknoracloud.go`、`internal/types/weknoracloud.go` | **R1 删除** |
| `frontend/src/utils/weknoraEntityLinks.ts`、`frontend/src/utils/weknoraEntityLinks.test.ts`、`frontend/src/views/chat/components/tool-results/WeKnoraDocResult.vue`、`packages/dsh-genyee/test/helpers/mock-weknora.mjs`、`website-docs/03-features/26-weknora-assistant.md` | R2 改名 |
| `frontend/public/weknora-widget.js` | R3：保留为兼容文件 |
| `mcp-server/weknora_mcp_server.py` | R3：改名 + 保留兼容入口 |
| `scripts/cloud-image/systemd/weknora.service`、`weknora-firstboot.service` | R3 |
| `frontend/src/assets/img/weknora.png`、`website-docs/homepage/public/docs/_home/brand/weknora-original.png`、`website-docs/public/videos/weknora-v0.9.0-en.mp4`、`.srt`、`-poster.png` | R5 |

---

## 四、WeKnora Cloud 删除规范

### 1. 决策

2026-11-12 由项目负责人确认：**WeKnora Cloud 服务已停用，相关功能从本分支彻底删除**。此决策替代 [BRANCH_GUIDE.md](BRANCH_GUIDE.md) 中原“保留代码、默认关闭（Opt-in）”的策略（BRANCH_GUIDE 已同步修订）。Langfuse 的 Opt-in 策略不变。

**代价（已接受）**：今后 `main → 本分支` 合并时，上游对 WeKnora Cloud 相关文件的修改会产生冲突，处理方式统一为“**保持删除**”（对已删除文件的修改直接丢弃；对共用文件中 Cloud 相关片段的修改不引入）。

### 2. 删除范围（生产代码 82 个文件 + 测试 24 个文件）

| 层 | 内容 |
|---|---|
| 后端 - 独立文件（整文件删除） | `internal/application/service/weknoracloud.go`、`internal/handler/weknoracloud.go`、`internal/infrastructure/docparser/weknoracloud_http_reader.go`、`internal/models/providers/weknoracloud.go`、`internal/models/providers/assets/weknoracloud.svg`、`internal/models/vlm/weknoracloud.go`、`internal/types/weknoracloud.go`、`internal/types/interfaces/weknoracloud.go` |
| 后端 - 共用文件（删除相关分支/字段） | 模型层：`internal/models/{api/embeddings.go, api/endpoint.go, api/cohererank/client.go, api/openaicompletions/request.go, asr/asr.go, chat/chat.go, embedding/embedder.go, rerank/reranker.go, vlm/vlm.go, utils/signer.go, providers/builtin.go, providers/definition.go}`；模型目录：`internal/models/catalog/data/{seed.json, models.generated.json}`（**`models.generated.json` 须用生成脚本重新生成**）；解析引擎：`internal/infrastructure/docparser/{doc.go, engines.go, engine_registry.go}`（`WeKnoraCloudEngineName`）；服务：`internal/application/service/{knowledge_process.go, model.go, model_catalog.go, temporary_document_parse.go, tenant.go}`；接口层：`internal/handler/{initialization.go, model_catalog.go, model_credentials.go, system.go, dto/model.go}`；路由与权限：`internal/router/{router.go, routes_infra.go, rbac.go}`；类型：`internal/types/{model.go, tenant.go, tenant_api_key.go, config_redaction.go, interfaces/tenant.go}`（`WeKnoraCloudCredentials`）；容器装配：`internal/container/container.go`；智能体：`internal/agent/tools/open_page.go` |
| 前端 | 整文件删除：`frontend/src/views/settings/WeKnoraCloudSettings.vue`、`frontend/src/utils/weknoraCloudModels.ts`；修改：`frontend/src/views/settings/{Settings.vue, ModelSettings.vue, ParserEngineSettings.vue}`、`frontend/src/views/system/ModelCatalog.vue`、`frontend/src/components/{ModelEditorDialog.vue, credentials/CredentialResource.vue}`、`frontend/src/config/settingsAccess.ts`、`frontend/src/api/model/index.ts`、6 个语言文件中的 `weknoracloud*` 文案键；`frontend/docker-entrypoint.sh` 中的 `GENYEE_WEKNORACLOUD_ENABLED` 注入；样式中的 `.engine-card--weknoracloud`、`.parser-engine-drawer--weknoracloud` |
| 配置与部署 | `docker-compose.yml`、`.env.example`、`.env.lite.example`、`helm/values.yaml`、`helm/templates/{app.yaml, frontend.yaml}`、`scripts/offline-bundle.sh` 中的 `GENYEE_WEKNORACLOUD_ENABLED` / `GENYEE_WEKNORACLOUD_TIMEOUT` |
| API 文档 | 删除 Go 注释后，用 swag **重新生成** `docs/docs.go`、`docs/swagger.json`、`docs/swagger.yaml` |
| 用户文档 | `website-docs/` 下 9 个文件中的 WeKnora Cloud 章节；`CHANGELOG.md` 历史条目**保留**（历史记录），可在新版本条目中注明“移除 WeKnora Cloud” |
| 测试 | 删除 Cloud 专属测试；修改共用测试中的 Cloud 用例 |
| **保留** | `migrations/versioned/000035_add_credentials.up.sql` 中的注释（P2）；数据库 `tenants.credentials` 列本身保留（还存放其他第三方凭证） |

### 3. 已有数据的处理（必须做，不能只删代码）

删除代码后，客户环境数据库中**可能已存在**引用 WeKnora Cloud 的数据。必须保证这些数据不会让系统报错：

| 数据 | 处理 |
|---|---|
| `models` 表中 `source` / 参数为 WeKnora Cloud 的模型 | 新增迁移：标记为不可用（或删除），并在日志中列出受影响的模型 ID；引用这些模型的知识库 / 智能体，在使用时报出明确的“模型已下线，请重新选择”，而不是 panic 或 500 |
| 知识库解析引擎选择为 `weknoracloud` | 运行时将未知引擎**回退为 `builtin`** 并打 WARN 日志；可选新增迁移批量改写 |
| `tenants.credentials` 中的 `weknoracloud` 字段 | 反序列化时忽略未知字段（确认 JSON 解析不会因未知字段失败）；可选新增迁移清除该字段，避免残留密钥 |
| 前端浏览器中缓存的 Cloud 相关设置 | 随 R3 存储迁移一并清理 |

> 本机部署环境已检查：`models` 与 `tenants.credentials` 中均**无** WeKnora Cloud 数据。客户环境需在升级前用同样的 SQL 自查。

### 4. 对外影响

- `/api/v1` 下 WeKnora Cloud 相关接口将返回 404。若有客户通过 API / SDK / CLI 调用过这些接口，需在发布说明中列出。
- 环境变量 `GENYEE_WEKNORACLOUD_ENABLED`、`GENYEE_WEKNORACLOUD_TIMEOUT` 失效（设置了也不再有作用，不会报错）。

---

## 五、实施批次

每个批次单独提交，提交信息注明批次号，例如 `refactor(rebrand): batch 2 - rename internal identifiers (WEKNORA_RESIDUE_REMEDIATION.md)`。

| 批次 | 内容 | 风险 | 依赖 |
|---|---|---|---|
| **B0** | 本规范文档；docreader gRPC 连接修复；`genyee-docreader` 文案修正 | 低 | — |
| **B1** | 删除 WeKnora Cloud（第四节），含数据兼容迁移 | 中 | B0 |
| **B2** | R2 直接替换：注释、日志、测试、示例域名、内部标识符、CSS、同页事件、Helm define、文件改名、Swagger 重新生成、文档站与 CLI 文档 | 低（量大） | B1 |
| **B3** | R3 兼容迁移：浏览器存储迁移模块、Redis 键、Docker 标签、沙箱路径（含沙箱镜像）、本地目录、工具名别名、Webhook 双头、嵌入脚本与消息协议、MCP 兼容入口、systemd | **高** | B2 |
| **B4** | R4 持久化默认名（按第八节决策执行）及升级文档 | **高** | B3，需先决策 |
| **B5** | R5 外部链接、品牌素材 | 中 | B2 |
| **B6** | 最终审计：导出源码包扫描，确认只剩白名单；更新本文档进度表 | — | 全部 |

---

## 六、验收标准

### 1. 每个批次提交前必须全部通过

```bash
# 后端
go build ./... && go vet ./... && go test ./...

# 前端（注意：必须带内存参数，否则 vite 构建会 OOM）
cd frontend
NODE_OPTIONS="--max-old-space-size=4096" npx vue-tsc --build
npx tsx --test $(find src -name "*.test.ts" -o -name "*.test.mjs")
npm run check-i18n
npm run build

# MCP Server / docreader（如本批次涉及）
cd mcp-server && python -m pytest
cd docreader && python -m pytest
```

### 2. 残留扫描（在导出包上执行，而不是在工作目录上）

```bash
rm -rf /tmp/genyee-export && mkdir -p /tmp/genyee-export
git archive --worktree-attributes HEAD | tar -x -C /tmp/genyee-export
cd /tmp/genyee-export
grep -rnI -i 'weknora' . | grep -v -f <(printf '%s\n' \
  '^./LICENSE' '^./THIRD_PARTY_NOTICES.md' '^./licenses/' '^./OFFLINE_LICENSES/' \
  '^./migrations/' '^./third_party/anydoc-go/' \
  'weknora-jwt-secret' 'weknora-system-aes-key-32bytes' \
  'legacy' 'Legacy')
```

### 3. 最终白名单（B6 完成后，扫描结果只允许出现以下内容）

| 允许保留 | 原因 |
|---|---|
| `LICENSE`、`THIRD_PARTY_NOTICES.md`、`licenses/`、`OFFLINE_LICENSES/` 中的上游署名 | P1 |
| `migrations/` 下已发布迁移文件 | P2 |
| 弱密钥黑名单中的旧占位值 | P4 |
| 命名为 `legacy*` 的兼容常量，以及读取旧值的兼容代码与其测试 | P3 兼容期 |
| `frontend/public/weknora-widget.js`、`mcp-server/weknora_mcp_server.py` 兼容入口 | P3 兼容期 |
| `X-WeKnora-Signature`、`X-WeKnora-Desktop-Token` 兼容代码 | P3 兼容期 |
| R4 中老环境锁定旧值的升级文档说明 | R4 |
| `third_party/anydoc-go/` | 第三方原文 |
| `CHANGELOG.md` 中的历史版本条目 | 历史记录 |

### 4. 回归验证（B3、B4 必做）

- 用**升级前版本**登录、设置偏好后，升级到新版本：不被登出、偏好仍在。
- 升级前创建的沙箱容器 / 快照，升级后能被正常回收。
- 嵌入了旧 `weknora-widget.js` 的页面，升级后仍可正常对话。
- 老 Webhook 接收方仍能用 `X-WeKnora-Signature` 验签通过。
- 历史会话中旧工具名（`search_weknora_docs`）的结果仍能正常渲染。
- 老环境按升级文档锁定旧集合名 / 前缀后，知识库检索结果、文件访问与升级前一致。

---

## 七、进度

| 批次 | 状态 | 提交 | 备注 |
|---|---|---|---|
| B0 | 已完成 | 与本文档同一提交 | 规范文档 + docreader gRPC 连接修复 + `genyee-docreader` 文案修正 + BRANCH_GUIDE 决策更新 |
| B1 | 未开始 | | |
| B2 | 未开始 | | |
| B3 | 未开始 | | |
| B4 | 未开始（待决策） | | |
| B5 | 未开始 | | |
| B6 | 未开始 | | |

---

## 八、待决策事项

| 编号 | 问题 | 选项 | 影响 |
|---|---|---|---|
| D1 | 目前是否已有**上线中的客户环境**？ | 有 / 没有 | 决定 R3 兼容期长短，以及 R4 能否直接改默认值 |
| D2 | R4 持久化默认名怎么处理？ | ① 新部署用 `genyee_*`，老环境锁定旧值（推荐）；② 全部保持 `weknora_*` 不变；③ 直接改（仅当 D1 为“没有”） | 改错会导致老环境数据“消失” |
| D3 | 内置智能体 ID `builtin-weknora-assistant` 是否改？ | ① 不改 ID，只改常量名（推荐）；② 新迁移改 ID | ② 需要数据迁移 |
| D4 | `X-WeKnora-Signature` 双头兼容保留多久？ | 一个大版本 / 指定日期 | 影响客户 Webhook 改造窗口 |
| D5 | `website-docs/` 是否作为 GENYEE 正式产品文档继续维护？ | ① 继续维护并完成品牌清理（推荐，内置助手依赖它回答产品问题）；② 删减为最小集 | 影响内置助手“查文档”能力的质量 |

---

## 附录：本次调研中确认的事实

- CLI 实际命令名已是 `genyee`（`cli/cmd/root.go:137`），但 `website-docs`、`CHANGELOG` 中仍有 `weknora auth login`、`weknora kb list` 等示例。
- 弱密钥黑名单位置：`internal/utils/crypto.go:18`、`internal/utils/presign.go:34`、`internal/utils/oidc_state.go:36`、`cmd/desktop/signing_key.go:77`。
- `config/builtin_agents.yaml:277` 定义内置助手 `builtin-weknora-assistant`；迁移 `000121`、`000133`（PostgreSQL）与 `000039`、`000050`（SQLite）直接引用了该 ID。
- 嵌入渠道 Webhook 签名头 `X-WeKnora-Signature` 写在迁移 `000060` 的列注释中。
- WeKnora Cloud 解析引擎名常量：`internal/infrastructure/docparser/engines.go:21` 的 `WeKnoraCloudEngineName = "weknoracloud"`。
- 内置助手的文档检索工具不联网，检索的是编译进二进制的 `website-docs/`（`internal/productdocs/library.go:19`），`website-docs/` 中的品牌残留会直接出现在助手回答里。
- `WEKNORACONFLUENCETABLE` 不是环境变量，而是 Confluence 导入时的文本内临时占位符（`internal/datasource/connector/confluence/markdown.go:67`）。
- `frontend/nginx.conf` 已同时提供 `/genyee-widget.js` 与 `/weknora-widget.js` 两个入口（第 45、51 行）。
- 本机部署数据库中，`models` 与 `tenants.credentials` 均无 WeKnora Cloud 相关数据（2026-11-12 检查）。
