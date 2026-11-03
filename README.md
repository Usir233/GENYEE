<p align="center">
  <a href="./website-docs/01-getting-started/01-introduction.md">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="./docs/images/readme/hero-en-dark.svg">
      <img src="./docs/images/readme/hero-en-light.svg" alt="GENYEE: find the answers, and put knowledge to work. Enterprise knowledge management framework for Q&A, tasks and wikis." width="100%">
    </picture>
  </a>
</p>

<p align="center">
  <a href="./website-docs/01-getting-started/01-introduction.md"><img alt="Docs" src="https://img.shields.io/badge/docs-read-b8863b?style=flat-square&labelColor=101f38"></a>
  <a href="./CHANGELOG.md"><img alt="Release" src="https://img.shields.io/badge/release-v0.9.0-b8863b?style=flat-square&labelColor=101f38"></a>
  <a href="./LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Commercial-b8863b?style=flat-square&labelColor=101f38"></a>
  <a href="https://github.com/Usir233/GENYEE/stargazers"><img alt="Stars" src="https://img.shields.io/github/stars/Usir233/GENYEE?style=flat-square&labelColor=101f38&color=b8863b"></a>
</p>

<p align="center">
  <b>English</b> · <a href="./README_CN.md">简体中文</a> · <a href="./README_JA.md">日本語</a> · <a href="./README_KO.md">한국어</a>
</p>

<p align="center">
  <a href="#overview">Overview</a> ·
  <a href="#quick-start">Quick Start</a> ·
  <a href="#whats-new">What's New</a> ·
  <a href="#features">Features</a> ·
  <a href="#clients-and-integrations">Clients</a> ·
  <a href="#documentation">Docs</a> ·
  <a href="#development">Development</a>
</p>

## Overview

[GENYEE](./website-docs/01-getting-started/01-introduction.md) is an LLM-powered knowledge framework for enterprise document understanding, semantic retrieval and reasoning. It brings a team's documents together so they can be searched, reasoned over and kept up to date.

https://github.com/user-attachments/assets/de0e490a-e738-405e-bb3a-64b63edd7278

<p align="center"><sub>v0.9.0 · 2:45 · 1080p60 · English narration and subtitles</sub></p>

Use RAG to look things up, the agent for multi-step tasks, and the wiki to organize knowledge. All three work on the same knowledge bases.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./docs/images/readme/capabilities-en-dark.svg">
  <img src="./docs/images/readme/capabilities-en-light.svg" alt="01 RAG: answers you can check, with hybrid search, multimodal parsing and citations. 02 Agent: tasks done with knowledge and tools, with multi-step reasoning, skills and sandbox, the local browser, MCP tools and memory. 03 Wiki: documents organized into a wiki, with a knowledge graph and rollback." width="100%">
</picture>

**The agent's toolbox.** Skills installed from ClawHub / SkillHub / Git / ZIP run in session-persistent Docker / E2B / Cube sandboxes, with an interactive terminal and graphical desktop beside the chat. Through the BrowserSkill extension the agent operates the user's own Chrome or Edge, and external MCP services (OAuth included) can be connected and enabled tool by tool.

Beyond the three modes:

- **Memory and curation**: cross-session long-term memory keeps the profile, preferences and facts a user has confirmed. Folder uploads keep their directory tree, and retrieval chunks can be edited, diffed and rolled back.
- **Data sources and formats**: auto-sync from Feishu wiki / Feishu Drive / Confluence / GitLab / Tencent IMA / Notion / Yuque / DingTalk Docs / Seafile / RSS, with more on the way. 10+ document formats including PDF, Word, images, Excel and XMind; Office files are parsed in-process by anydoc.
- **Channels and integrations**: Q&A in WeCom, Feishu, Slack, Telegram and other IM apps; an embed widget for external websites; a built-in MCP Server for Cursor, Claude and other AI tools; scoped API keys with a principal model for programmatic access.
- **Models**: 29 built-in vendors with a generated model catalog, including OpenAI, DeepSeek, Qwen (Alibaba Cloud), Zhipu, Hunyuan, Gemini, MiniMax, NVIDIA, LiteLLM and Ollama.
- **Permissions and operations**: multi-workspace RBAC (four roles, per-resource ownership, per-workspace audit log), several storage instances per workspace, a runtime task-queue dashboard with worker-pool governance, and Langfuse tracing for agent steps, token usage and pipelines.
- **Deployment**: LLMs, vector databases and storage backends are all swappable. Deploy locally or on a private cloud and keep the data in your own environment.

## Quick Start

Deploy GENYEE in your own environment with Docker Compose or Kubernetes and configure models, storage and networking yourself.

### Run with Docker Compose

Requires [Docker](https://www.docker.com/), [Docker Compose](https://docs.docker.com/compose/) and [Git](https://git-scm.com/).

```bash
git clone https://github.com/Usir233/GENYEE.git
cd GENYEE
cp .env.example .env    # Edit .env as needed, see comments in the file
docker compose pull     # Pull the latest images
docker compose up -d    # Start core services
```

Then open **http://localhost** and follow the onboarding guide. A walkthrough with sample data is in the [Quickstart](./website-docs/01-getting-started/03-quickstart.md).

> [!TIP]
> To use a local Ollama model, run `ollama serve > /dev/null 2>&1 &` first. For the Ollama embedding model name, `OLLAMA_BASE_URL`, and RAM notes, see [Configuration](./website-docs/01-getting-started/04-configuration.md).

| Service | URL |
|---------|-----|
| Web UI | `http://localhost` |
| Backend API | `http://localhost:8080` |
| Langfuse Tracing | `http://localhost:3000` |

### Optional services

Add `--profile` flags to enable additional components; multiple profiles can be combined.

| Profile | Adds |
|---------|------|
| _(default)_ | Core services |
| `full` | All features |
| `neo4j` | Knowledge Graph (Neo4j) |
| `minio` | Object Storage (MinIO) |
| `langfuse` | Tracing (Langfuse) |

```bash
docker compose --profile neo4j --profile minio pull
docker compose --profile neo4j --profile minio up -d
docker compose down     # Stop services
```

### Upgrading

If you already have GENYEE running and downloaded a newer release:

```bash
# Set WEKNORA_VERSION in .env to the target release (e.g. 0.9.0), or keep latest
docker compose pull     # Pull images matching WEKNORA_VERSION
docker compose up -d    # Recreate containers with new images
```

> [!NOTE]
> `docker compose up -d` alone reuses locally cached images and may leave the UI version out of sync with the release you downloaded. Read the [upgrade notes](./website-docs/07-releases/v0.9.0.md#upgrade-notes) before upgrading. Deployments older than v0.8.2 should also read the [v0.8.2 notes](./website-docs/07-releases/v0.8.2.md#upgrade-notes).

### Other ways to deploy

| Option | When to use it |
|--------|----------------|
| **Docker Compose** | The standard deployment above: all features, multiple services |
| **Kubernetes (Helm)** | Production clusters; the chart is in [`helm/`](./helm) |
| **Lite single binary** | Local or low-resource use with no external dependencies (SQLite + in-memory queue); see [Lite vs. standard](./docs/LITE.md) |
| **Desktop app** | The Lite runtime with a GUI, login-free start and a macOS host sandbox; no installer is published yet, so build it from source |

All options, hardware requirements and deployment topologies: [Installation guide](./website-docs/01-getting-started/02-installation.md).

> [!WARNING]
> GENYEE ships with login authentication, but for production deployments we strongly recommend that you:
> - deploy it in an internal / private network rather than on the public internet;
> - avoid exposing the service directly to public networks, to prevent information leakage;
> - configure proper firewall rules and access controls for the deployment environment;
> - regularly update to the latest version for security patches and improvements.

## What's New

### v0.9.0 <sub>· 2026-10-29 · [release notes](./website-docs/07-releases/v0.9.0.md)</sub>

- **[GENYEE assistant](./website-docs/07-releases/v0.9.0.md#assistant)**: Ask about GENYEE releases, features and configuration, then work from the current page: create agents, schedule tasks and manage knowledge, acting as you within your permissions.
- **[Multi-agent collaboration](./website-docs/07-releases/v0.9.0.md#collaboration)**: Delegate independent subtasks, follow progress and bring the results together.
- **[Automations](./website-docs/07-releases/v0.9.0.md#automations)**: Run one-off or recurring tasks with execution history, results and approval notifications.
- **[Execution permissions](./website-docs/07-releases/v0.9.0.md#permissions)**: Read-only, confirm-before-change and automatic-write modes, with scoped session and automation grants.
- **[Sandboxes and background work](./website-docs/07-releases/v0.9.0.md#sandbox)**: Optional per-user sandbox reuse, managed background shell jobs and skill rollout controls.
- **[Parsing, sync and retrieval](./website-docs/07-releases/v0.9.0.md#knowledge)**: Configurable image pipelines, OCR preprocessing, deeper Feishu/Lark sync, Confluence PAT and retrieval improvements.
- **[Upgrade notes](./website-docs/07-releases/v0.9.0.md#upgrade-notes)**: Review database migrations, Feishu resync, the sandbox allocation gate and configuration changes.

### v0.8.3 <sub>· 2026-10-14 · [release notes](./website-docs/07-releases/v0.8.3.md)</sub>

- **[Image gallery](./website-docs/07-releases/v0.8.3.md#images)**: browse document images and filter by source, caption, OCR and image attributes.
- **[Multimodal Embedding](./website-docs/07-releases/v0.8.3.md#multimodal-embedding)**: support for multimodal embedding models that encode text and images into a shared vector space, including Tencent WeMM-Embedding and Qwen3-VL-Embedding, with opt-in image-vector retrieval and compatible image-aware reranking.
- **[Wiki graph](./website-docs/07-releases/v0.8.3.md#wiki-graph)**: clearer group layouts and theme captions, with group focus, switching and return to the full graph.
- **[Source citations](./website-docs/07-releases/v0.8.3.md#conversation)**: navigate from an answer to the cited passage in the original document.
- **[Chat and tools](./website-docs/07-releases/v0.8.3.md#tools)**: on-demand attachment reading without a sandbox, unread replies, @ mention search, Docker terminals, Lite host skills and MCP interoperability updates.
- **[Administration and integrations](./website-docs/07-releases/v0.8.3.md#administration)**: platform user management, invitation-only signup mode, administrator model catalog, WeMM embeddings, TEI/Pinecone/Cohere reranking, Seafile and Traditional Chinese.
- **[Reliability](./website-docs/07-releases/v0.8.3.md#other-changes)**: broad retrieval, parsing, datasource, Wiki, and OAuth fixes. Review [upgrade requirements](./website-docs/07-releases/v0.8.3.md#upgrade-notes) for registration modes, empty tool lists, database migrations and reindexing.

### v0.8.2 <sub>· 2026-09-24 · [release notes](./website-docs/07-releases/v0.8.2.md)</sub>

Agents can operate the browser on your computer, knowledge bases can be published to other AI tools over MCP, and a running conversation can be steered, forked or rewound.

- **[Local Browser (BrowserSkill)](./website-docs/07-releases/v0.8.2.md#local-browser)**: agents drive the user's own Chrome / Edge through the open-source BrowserSkill extension, with a live task preview, pause / resume and hand-off for logins and CAPTCHAs.
- **[Built-in MCP Server](./website-docs/07-releases/v0.8.2.md#mcp-server)**: per-workspace `/mcp/<endpoint_id>` endpoints over Streamable HTTP, each with its own token, knowledge-base scope, rate limit and tool groups. The Python `mcp-server/` is deprecated.
- **[Conversation control](./website-docs/07-releases/v0.8.2.md#conversation-control)**: append requirements to a running turn, fork from any earlier question, rewind in place with sandbox checkpoints, and pick reasoning effort per session. Generated files are collected in a new artifacts library.
- **[Sandbox](./website-docs/07-releases/v0.8.2.md#sandbox)**: interactive terminal and graphical desktop; macOS Lite host sandbox with project folders; a sidebar Toolbox for skills, MCP services and the browser connection.
- **[Models](./website-docs/07-releases/v0.8.2.md#models)**: rebuilt model catalog (27 built-in vendors with generated context-window, max-output, reasoning and vision metadata). Agent retrieval tools are consolidated into `search_knowledge` / `read_document` / `list_documents`.
- **[Knowledge and platform](./website-docs/07-releases/v0.8.2.md#knowledge)**: Confluence and DingTalk Docs data sources; Bocha and Serply web search; Japanese UI; per-channel IM reply language; whitelist-only outbound mode.

> [!IMPORTANT]
> **Breaking:** DingTalk channels are Stream-only, and sandbox commands run as `root`. See the [upgrade notes](./website-docs/07-releases/v0.8.2.md#upgrade-notes).

### v0.8.0 <sub>· [release notes](./website-docs/07-releases/v0.8.0.md)</sub>

- **Skill sandbox runtime**: session-persistent Docker / E2B / Cube backends with per-tenant network policy; the Local host-process backend is removed and Docker is opt-in.
- **Tenant skill catalog**: install from ClawHub / SkillHub / git / zip, with per-sandbox snapshots, live progress, file browse/edit, and personal and workspace env vars.
- **Cross-session long-term memory**: profile / preference / fact / task / interest, auto-extracted with user confirmation, plus `search_memory`.
- **Parsing and sources**: in-process anydoc office parser; GitLab and Tencent IMA data sources; XMind parsing.
- **Ecosystem**: DeepSeek Harness plugin (`./packages/dsh-weknora`); LiteLLM; Exa and Metaso web search.
- **Chat**: chat artifacts, question outline and timestamps; context compaction and provider prompt-cache markers.
- **Security**: OIDC JWKS verification, optional complex passwords, document auto-tagging, and broad sandbox/security hardening.

<details>
<summary><b>Earlier releases (v0.2.0 – v0.7.2)</b></summary>

<br/>

- **v0.7.2** — Product documentation site; knowledge base folder tree; chunk editing with revision history; wiki page revisions; directly loadable file URLs (`resource_urls=public`); Feishu Drive data source; batch tagging; MCP Server 1.1 (29 tools); AWS S3 default credential chain.
- **v0.7.1** — Yunzhijia IM; Volcengine rerank; Zhipu AI web search; platform-scoped API keys; per-KB activity audit; FAQ filtering, tagging and export; Langfuse OTLP tracing; one-click Markdown export.
- **v0.7.0** — Scoped API keys and principal model; task-queue dashboard and worker-pool governance; multiple storage instances per workspace; temporary chat attachments; `@Skill / @MCP` mentions; mid-conversation MCP OAuth; QQBot and Lark IM; Redis TLS; `weknora` CLI v0.10.
- **v0.6.3** — Website embed widget & Integrations Center (secure-mode token exchange + rate limits); chat experience overhaul (citation popovers, RAG pipeline progress, streaming markdown); document multi-tag & batch reparse; Wiki folders & hierarchy navigation; RSS data source; MCP OAuth2; EPUB / MHTML parsing; agent model-readiness checks; model test debugger; session source filter; workspace deletion UI.
- **v0.6.2** — Per-upload process configuration with upload-confirm dialog; document reparse with `process_config`; `weknora` CLI v0.9 (bundled Agent Skills, `session stop`, auth/profile harmonization); KB marquee multi-select; HNSW index for 1024-dim pgvector embeddings; chat resources store refactor; Langfuse-only tracing (Jaeger removed).
- **v0.6.1** — Document parsing trace timeline (Langfuse-style span tree with stage-by-stage progress + stop-parse); OpenSearch vector store driver; declarative built-in models via YAML; system admin & consolidated platform settings + audit log; new-user onboarding guide; settings UI redesign; `weknora` CLI v0.7 / v0.8 (agent-first wire contract, NDJSON, `--dry-run`); OpenDataLoader + PaddleOCR-VL parsers; MCP server multi-transport (stdio / SSE / HTTP); per-model thinking-mode config; Tencent LKEAP rerank + native Gemini embeddings + MiniMax-M3.
- **v0.6.0** — Workspace RBAC (4-tier role matrix `Owner` / `Admin` / `Contributor` / `Viewer` + per-KB ownership + per-workspace audit log), workspace member management & multi-workspace UX, self-service workspaces; `weknora` CLI v0.4 GA with `mcp serve`; KB retrieval fan-out across vector stores; AES-256-GCM credential encryption + docreader gRPC TLS + Token; Zhipu embedder + Huawei OBS; server-side user preferences; Go 1.26.0. See [Tenants & auth](./website-docs/03-features/01-tenant-auth.md).
- **v0.5.2** — Wiki ingest scales to 40k-document KBs (task queue + DLQ); MCP human-in-the-loop tool approval; Anthropic / Apache Doris / Tencent VectorDB / KS3 / SearXNG backends; adaptive 3-tier chunking with live preview; global ⌘K command palette; Yuque connector + WeChat Mini Program; `weknora` CLI preview.
- **v0.5.1** — Knowledge-base batch management; workspace-wide IM channels overview; session search + user-scoped pinning; unified Model / Web Search / MCP settings cards; per-agent LLM timeout; desktop workspace switching.
- **v0.5.0** — Wiki Mode GA — agents auto-generate structured, interlinked Markdown wiki pages with a knowledge graph; wiki browser + visual graph in the UI.
- **v0.4.0** — GENYEE Cloud (hosted LLM + parsing); Chrome Extension; ClawHub Skill; WeChat IM; attachment processing; Azure OpenAI / Alibaba OSS; Notion connector; Baidu + Ollama web search; VectorStore management.
- **v0.3.6** — ASR (audio); Feishu data-source auto-sync; OIDC; IM quote-reply context + thread-based sessions; document summarization; Tavily search; parallel tool calling; agent @mention scope restriction.
- **v0.3.5** — Telegram / DingTalk / Mattermost IM; IM slash commands + QA queue; suggested questions; VLM auto-describe MCP tool images; Novita AI; channel tracking.
- **v0.3.4** — WeCom / Feishu / Slack IM; multimodal image support; NVIDIA model API; Weaviate; AWS S3; AES-256-GCM API-key encryption; built-in MCP service; hybrid-search optimization; `final_answer` tool.
- **v0.3.3** — Parent-child chunking; KB pinning; fallback response; passage cleaning for rerank; storage auto-creation; Milvus.
- **v0.3.2** — Knowledge Search entry; per-source parser & storage engine config; image rendering in local storage; document preview; Volcengine TOS; Mermaid rendering; batch session management; memory graph preview.
- **v0.3.0** — Shared Space; Agent Skills + sandboxed execution; custom agents; Data Analyst agent; thinking mode; Bing / Google web search; API Key auth; Helm chart; Korean i18n; Qdrant.
- **v0.2.0** — Agent Mode (ReACT); multi-type knowledge bases (FAQ + document); conversation strategy config; DuckDuckGo web search; MCP tool integration; new UI with agent mode switching; MQ async task management.

Full history: [`CHANGELOG.md`](./CHANGELOG.md).

</details>

## Product Tour

### Quick Q&A and smart reasoning

**Two ways to ask.** Quick Q&A answers from the knowledge base with RAG and cites the sources it used. In smart reasoning the agent plans multi-step work, searching, reading documents and calling tools and skills, and shows each step in the conversation. [Docs →](./website-docs/03-features/18-chat-experience.md)

<a href="./docs/images/readme/spotlight-qa-light.webp">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./docs/images/readme/spotlight-qa-dark.webp">
  <img src="./docs/images/readme/spotlight-qa-light.webp" alt="Quick Q&A and smart reasoning" width="100%">
</picture>
</a>

### Local browser

**Operate the browser on your computer.** Through the open-source BrowserSkill extension, the agent opens pages and fills in forms in your own Chrome or Edge, and hands over to you for logins and CAPTCHAs. [Docs →](./website-docs/05-clients/09-local-browser.md)

<a href="./docs/images/readme/spotlight-browser-light.webp">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./docs/images/readme/spotlight-browser-dark.webp">
  <img src="./docs/images/readme/spotlight-browser-light.webp" alt="Local browser" width="100%">
</picture>
</a>

### Skills and sandbox

**Run skills and produce files.** Docker, E2B and Cube backends are supported. Turns in the same session share one workspace, and generated files can be previewed and downloaded. Open the graphical desktop or interactive terminal beside the chat to follow each step and take over when needed. [Docs →](./website-docs/03-features/22-skills-sandbox.md)

<a href="./docs/images/readme/spotlight-sandbox-light.webp">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./docs/images/readme/spotlight-sandbox-dark.webp">
  <img src="./docs/images/readme/spotlight-sandbox-light.webp" alt="Skills and sandbox" width="100%">
</picture>
</a>

### Toolbox: MCP services and skills

**Tools the agent can use.** Connect external MCP services and choose, tool by tool, which are enabled and which calls need approval. Install skills from ClawHub, SkillHub, Git or ZIP, manage them per workspace, and reuse them across sandboxes. [Docs →](./website-docs/03-features/22-skills-sandbox.md)

<a href="./docs/images/readme/spotlight-toolbox-light.webp">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./docs/images/readme/spotlight-toolbox-dark.webp">
  <img src="./docs/images/readme/spotlight-toolbox-light.webp" alt="Toolbox: MCP services and skills" width="100%">
</picture>
</a>

### Automatic wiki

**Documents organized into a browsable wiki.** With Wiki enabled, GENYEE extracts people, products and concepts from knowledge-base documents into pages with source citations, organized by directory. The knowledge graph shows how pages relate; pages can be edited directly and every change can be rolled back. [Docs →](./website-docs/03-features/14-wiki.md)

<a href="./docs/images/readme/spotlight-wiki-light.webp">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./docs/images/readme/spotlight-wiki-dark.webp">
  <img src="./docs/images/readme/spotlight-wiki-light.webp" alt="Automatic wiki" width="100%">
</picture>
</a>

### Observability

**Tracing and runtime monitoring.** Langfuse traces the reasoning, tool calls and token usage of each agent step. The document parsing timeline shows progress stage by stage, and the task-queue dashboard lists queued and failed tasks. [Docs →](./website-docs/03-features/16-observability.md)

<a href="./docs/images/readme/spotlight-observability-light.webp">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./docs/images/readme/spotlight-observability-dark.webp">
  <img src="./docs/images/readme/spotlight-observability-light.webp" alt="Observability" width="100%">
</picture>
</a>

## Architecture

<a href="./docs/images/readme/architecture-en-light.svg">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./docs/images/readme/architecture-en-dark.svg">
  <img src="./docs/images/readme/architecture-en-light.svg" alt="GENYEE architecture: clients and channels connect to the GENYEE app, where RAG Q&A, agent reasoning and auto wiki share one knowledge pipeline; the app calls runtime services and stores data in PostgreSQL, Redis and optional stores" width="100%">
</picture>
</a>

A modular pipeline from document parsing, vectorization and retrieval to LLM inference, in which every component can be replaced or extended. It runs locally or on a private cloud, and the Web UI needs no setup to get started. More: [Architecture overview](./website-docs/02-architecture/01-overview.md) · [RAG pipeline](./website-docs/02-architecture/04-rag-pipeline.md) · [Extension points](./website-docs/06-development/03-extension-points.md).

## Features

| Area | Highlights |
|------|------------|
| [Q&A and agent](./website-docs/03-features/07-agent.md) | Quick Q&A answers from knowledge bases with citations; smart reasoning runs a ReAct agent over knowledge bases, web search, MCP tools, skills and the local browser. Steer, fork or rewind a running conversation, and keep [long-term memory](./website-docs/03-features/23-memory.md) across sessions |
| [Wiki](./website-docs/03-features/14-wiki.md) | Agent-generated, interlinked wiki pages with a knowledge graph; in-browser editing, revision diff and rollback |
| [Skills and sandbox](./website-docs/03-features/22-skills-sandbox.md) | Skill catalog installed from ClawHub / SkillHub / Git / ZIP; session-persistent Docker / E2B / Cube sandboxes with network policy; terminal and graphical desktop beside the chat |
| [Knowledge bases](./website-docs/03-features/02-knowledge-base.md) | FAQ, document and wiki bases; folder tree; chunk editing with revisions; per-upload parsing, chunking and multimodal settings; auto-tagging |
| [Retrieval](./website-docs/03-features/05-retrieval-engines.md) | Keyword + vector hybrid search, rerank, parent-child chunking and GraphRAG ([Neo4j](./website-docs/03-features/09-knowledge-graph.md)); end-to-end evaluation with recall and BLEU / ROUGE |
| [Access and security](./website-docs/03-features/01-tenant-auth.md) | Workspace RBAC with four roles and an audit log; scoped API keys; OIDC; AES-256-GCM credential encryption; SSRF-safe outbound requests with a whitelist-only mode |
| [Operations](./website-docs/03-features/16-observability.md) | Langfuse tracing for agent steps, tokens and pipelines; document parsing timeline; task-queue dashboard with worker pools; automatic migrations on upgrade |

### Supported backends

| Component | Options |
|-----------|---------|
| [LLMs](./website-docs/03-features/06-models.md) | 29 built-in vendors, including OpenAI / Azure OpenAI / Anthropic / DeepSeek / Qwen / Zhipu / Hunyuan / Doubao / Gemini / MiniMax / NVIDIA / SiliconFlow / OpenRouter / LiteLLM / Ollama |
| Embeddings | Ollama / BGE / GTE / Zhipu / OpenAI-compatible APIs |
| Vector databases | PostgreSQL (pgvector + ParadeDB) / Elasticsearch / OpenSearch / Milvus / Weaviate / Qdrant / Apache Doris / Tencent VectorDB |
| [Object storage](./website-docs/03-features/19-storage-backends.md) | Local / Tencent Cloud COS / MinIO / AWS S3 / Volcengine TOS / Alibaba Cloud OSS / Kingsoft Cloud KS3 / Huawei Cloud OBS |
| [Document formats](./website-docs/03-features/03-document-parsing.md) | PDF / Word / PPT / Excel / CSV / TXT / Markdown / HTML / EPUB / MHTML / JSON / XMind / images |
| [Data sources](./website-docs/03-features/10-datasource.md) | Feishu wiki / Feishu Drive / Lark / Confluence / GitLab / Tencent IMA / Notion / Yuque / DingTalk Docs / Seafile / RSS |
| [IM channels](./website-docs/03-features/12-im-integration.md) | WeCom / Feishu / Lark / QQBot / Slack / Telegram / DingTalk / Mattermost / WeChat / Yunzhijia |
| [Web search](./website-docs/03-features/11-web-search.md) | DuckDuckGo / Bing / Google / Tavily / Baidu / Ollama / SearXNG / Keenable / Zhipu AI / Exa / Metaso / Bocha / Serply / SearchApi |
| Deployment | Docker Compose / Kubernetes (Helm) / Lite single binary / desktop app; offline and private-cloud installs; UI in Chinese, English, Japanese, Korean and Russian |

## Clients and Integrations

| | Client | What it does |
|:-:|--------|--------------|
| <img src="./docs/images/readme/icons/plug.svg" width="22" height="22" alt=""> | [**Built-in MCP Server**](./website-docs/03-features/08-mcp.md) | Publishes knowledge bases to Cursor, Claude and other MCP clients over Streamable HTTP; the Python [`mcp-server/`](./mcp-server/MCP_CONFIG.md) is deprecated |
| <img src="./website-docs/homepage/public/docs/_home/brands/browserskill.png" width="22" height="22" alt=""> | [**Local Browser (BrowserSkill)**](./website-docs/05-clients/09-local-browser.md) | Lets agents operate the user's own Chrome / Edge |
| <img src="./docs/images/readme/icons/phone.svg" width="22" height="22" alt=""> | [**WeChat Mini Program**](./miniprogram/README.md) | Lightweight mobile client: configure API access, select knowledge bases, import URLs, and ask knowledge chat from WeChat |
| <img src="./website-docs/homepage/public/docs/_home/brands/deepseek-color.svg" width="22" height="22" alt=""> | [**DeepSeek Harness plugin**](./website-docs/05-clients/08-deepseek-harness.md) | Gives `dsh` coding agents four read-only tools: search, read document, ask and list knowledge bases |
| <img src="./docs/images/readme/icons/code.svg" width="22" height="22" alt=""> | [**Website Embed Widget**](./website-docs/03-features/13-embed-channel.md) | Publishes agents on external sites |
| <img src="./docs/images/readme/icons/braces.svg" width="22" height="22" alt=""> | [**Go SDK**](./website-docs/05-clients/03-go-sdk.md) | CRUD for knowledge bases, documents and sessions, plus SSE streaming Q&A |

## Documentation

The full product documentation lives in [**`website-docs/`**](./website-docs/01-getting-started/01-introduction.md) (in Chinese), organized as Getting Started → Architecture → Features → API → Clients → Development and covering ~360 API endpoints and ~150 environment variables.

| Start here | |
|------------|---|
| [Introduction](./website-docs/01-getting-started/01-introduction.md) | Capabilities overview |
| [Installation](./website-docs/01-getting-started/02-installation.md) | Docker Compose, Helm, Lite and desktop |
| [Configuration](./website-docs/01-getting-started/04-configuration.md) | Environment variables and models |
| [Troubleshooting FAQ](./website-docs/01-getting-started/05-troubleshooting.md) | Common problems and fixes |
| [API reference](./website-docs/04-api/01-api-overview.md) | REST API overview |
| [Release notes](./website-docs/07-releases/v0.9.0.md) | What changed in each release |

## Development

If you need to frequently modify code, you don't need to rebuild Docker images every time. Use fast development mode:

```bash
make dev-start      # Start infrastructure
make dev-app        # Start backend (new terminal)
make dev-frontend   # Start frontend (new terminal)
```

- Frontend modifications auto hot-reload (no restart needed)
- Backend modifications quick restart (5-10 seconds, supports Air hot-reload)
- No need to rebuild Docker images
- Supports IDE breakpoint debugging

See the [Development guide](./website-docs/06-development/01-dev-guide.md) for details.

The documentation site and product homepage are built from [`website-docs/`](./website-docs/README.md). With Node.js 24, run `cd website-docs && npm run setup && npm run build && npm run preview` to preview both together; the unified static output serves the homepage at `/` and documentation at `/docs/`. See the directory's README for Nginx and Docker deployment.

## Contributing

Welcome to submit [Issues](https://github.com/Usir233/GENYEE/issues) or Pull Requests.

- **Process:** Fork → Create branch → Commit changes → Open PR
- **Standards:** Format code with `gofmt`, follow [Conventional Commits](https://www.conventionalcommits.org/) (`feat:` / `fix:` / `docs:` / `test:` / `refactor:`)

<details>
<summary><b>Validating your change</b></summary>

<br/>

For a focused PR, validate the changed scope first:

```bash
git fetch origin main
git diff --check origin/main...HEAD
golangci-lint run --new-from-rev=origin/main ./...
go test ./path/to/changed/package -count=1
```

Run `gofmt` on changed Go files before committing. For frontend changes, run the relevant tests from `frontend/` and use `npm run type-check` when the change affects TypeScript or Vue components.

The full maintainer gate remains:

```bash
make fmt
make lint
make test
```

`make fmt` formats the entire Go repository, so run it only with a clean worktree and review the resulting diff. Some full-suite tests require local infrastructure or service configuration. If a full check fails for an unrelated baseline or environment reason, include the exact command and failure in the PR while still providing passing targeted tests for your change.

</details>

### Contributors

Thanks to everyone who has contributed:

[![Contributors](https://contrib.rocks/image?repo=Usir233/GENYEE)](https://github.com/Usir233/GENYEE/graphs/contributors)

## License

GENYEE is commercial software licensed under the [GENYEE Commercial License](./LICENSE).

**Attribution & Non-Affiliation Notice:** GENYEE is built upon the open-source project [Tencent WeKnora](https://github.com/Tencent/WeKnora) (`Copyright (C) 2026 Tencent`, licensed under the MIT License) and other open-source components; see [`THIRD_PARTY_NOTICES.md`](./THIRD_PARTY_NOTICES.md) for the complete upstream MIT license and third-party notices. GENYEE is an independent commercial distribution and is not affiliated with, endorsed by, or sponsored by Tencent. `WeKnora`, `Tencent`, and `WeChat` are trademarks of Tencent.
