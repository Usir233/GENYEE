# GENYEE 离线交付包容器镜像开源许可证清单 (Offline Bundle License Matrix)

本目录随 GENYEE 离线交付包（Air-Gapped Bundle）一同分发，详细列出离线包内各容器镜像的开源许可证类型、与 GENYEE 主程序的隔离边界，以及依据许可证条款提供的源代码获取方式。

---

## 一、架构隔离与许可证传染性说明

1. **进程与网络隔离（Aggregate / Mere Aggregation）**：
   - GENYEE 后端主程序（`genyee-app`）与前端（`genyee-ui`）采用 **MIT 许可证**授权。
   - 基础设施容器（PostgreSQL/ParadeDB、Redis、MinIO、Qdrant、Neo4j、SearXNG 等）作为独立容器进程运行，仅通过标准网络协议（SQL wire protocol、RESP、S3 HTTP API、gRPC、Bolt、REST JSON）与 `genyee-app` 通信。
   - 依据 GPLv3 / AGPLv3 第 5 条关于“纯粹聚合（Mere Aggregation）”之规定，将未修改的第三方基础设施镜像与 GENYEE 镜像置于同一离线压缩包或介质中分发，不会将 GENYEE 自研代码传染为 GPL/AGPL。
2. **完整合规配套**：
   - 对于离线包中包含的所有采用 **AGPLv3 / GPLv3 / GPLv2 / LGPL** 许可的组件（包括未修改的第三方基础设施容器，以及 `genyee-docreader` / `genyee-sandbox` 容器内的系统级开源工具包），本目录随附对应的许可证全文引用及有效期不少于三（3）年的书面源代码提供要约（详见 [WRITTEN_OFFER.md](WRITTEN_OFFER.md)）。

---

## 二、离线交付包镜像许可证矩阵

### 1. GENYEE 自研组件镜像

| 镜像名称 | 默认标签 | 角色说明 | 自研代码许可证 | 容器内含第三方系统工具说明 |
| :--- | :--- | :--- | :--- | :--- |
| `${GENYEE_REGISTRY}/genyee-app` | `latest` | Go 后端 API 与任务引擎 | MIT | Debian 基础运行库 + ONNX Runtime (MIT) |
| `${GENYEE_REGISTRY}/genyee-ui` | `latest` | Vue 3 前端 Web 界面 | MIT | Nginx (BSD-2-Clause) + 前端依赖 (见 `THIRD_PARTY_NOTICES.md`) |
| `${GENYEE_REGISTRY}/genyee-docreader` | `latest` | 文档解析 gRPC 服务 | MIT | 含未修改的 LibreOffice (MPL-2.0)、Poppler (`pdftoppm`, GPL-2.0/3.0)、Antiword (GPL) 等系统包，仅通过独立子进程 CLI 调用 |
| `${GENYEE_REGISTRY}/genyee-sandbox` | `latest` | 代码执行沙箱容器 | MIT | 含 Debian 系统工具、Python/Node 运行时及文档处理 CLI 工具 |
| `${GENYEE_REGISTRY}/genyee-odl-hybrid` | `latest` | 可选混合文档解析服务 | MIT | Python / Docling 运行时环境 |
| `${GENYEE_REGISTRY}/genyee-mcp` | `latest` | 可选 MCP Server | MIT | Python MCP SDK (MIT) |

### 2. 核心与可选基础设施镜像

| 镜像名称 | 固定版本标签 | 所属 Profile | 许可证 | 是否需要源码要约 | 备注 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `paradedb/paradedb` | `v0.25.11-pg17` | 核心必选 | **AGPL-3.0-only** | 是（见 `WRITTEN_OFFER.md`） | 仅通过 PostgreSQL 5432 协议通信；未修改上游镜像 |
| `redis` | `7.0-alpine` / `7.2-alpine` | 核心必选 | **BSD-3-Clause** | 否 | 固定在 7.2 及以下版本（Redis 7.4+ 变更为 RSALv2/SSPLv1） |
| `pgsty/minio` | `RELEASE.2026-08-04T00-00-00Z` | `minio` / `langfuse` | **AGPL-3.0-only** | 是（见 `WRITTEN_OFFER.md`） | 仅通过标准 S3 HTTP API 通信；未修改上游镜像 |
| `qdrant/qdrant` | `v1.16.2` | `qdrant` | **Apache-2.0** | 否 | 向量数据库，gRPC/REST 通信 |
| `neo4j` | `2025.10.1` | `neo4j` | **GPL-3.0 / AGPL-3.0** | 是（见 `WRITTEN_OFFER.md`） | 图数据库，Bolt 协议通信 |
| `milvusdb/milvus` | `v2.6.11` | `milvus` | **Apache-2.0** | 否 | 向量数据库 |
| `semitechnologies/weaviate` | `1.28.4` | `weaviate` | **BSD-3-Clause** | 否 | 向量数据库 |
| `apache/doris` | `fe-4.1.0` / `be-4.1.0` | `doris` | **Apache-2.0** | 否 | 分析型检索数据库 |
| `busybox` | `1.36` | `searxng` | **GPL-2.0-only** | 是（见 `WRITTEN_OFFER.md`） | SearXNG 配置初始化一次性容器 |
| `searxng/searxng` | `2025.5.18-5286903` | `searxng` | **AGPL-3.0-or-later** | 是（见 `WRITTEN_OFFER.md`） | 可选元搜索引擎，HTTP JSON 通信 |
| `dexidp/dex` | `v2.41.1` | `dex` | **Apache-2.0** | 否 | 可选 OIDC 身份提供者 |
| `clickhouse/clickhouse-server` | `24.8.14.39` | `langfuse` | **Apache-2.0** | 否 | Langfuse 自建栈 OLAP 存储 |
| `langfuse/langfuse` | `3.29.0` | `langfuse` | **MIT (Core) / Commercial (`ee/`)** | 否 | 默认关闭；自建仅使用开源特性，不得激活 `ee/` 商业授权特性 |
| `langfuse/langfuse-worker` | `3.29.0` | `langfuse` | **MIT (Core) / Commercial (`ee/`)** | 否 | 同上 |

---

## 三、相关合规文件索引

- 根目录 [`LICENSE`](../LICENSE)：GENYEE 项目 MIT 许可证全文
- 根目录 [`NOTICE`](../NOTICE)：项目归属与演进声明
- 根目录 [`THIRD_PARTY_NOTICES.md`](../THIRD_PARTY_NOTICES.md)：Go、前端 npm、Python 及 Rust 第三方依赖许可证清单
- 本目录 [`WRITTEN_OFFER.md`](WRITTEN_OFFER.md)：GPL / AGPL / LGPL 对应源代码（Corresponding Source）书面获取要约
