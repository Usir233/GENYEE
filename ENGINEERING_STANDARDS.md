# GENYEE Engineering Standards (v3 Permanent Specification)

> **Governance Scope & Export Policy**:
> This document defines the permanent, non-negotiable engineering, architectural, static-analysis, contract, modern-web, resource-safety, and compliance standards for the `GENYEE` repository (`github.com/Usir233/GENYEE`). All contributions, automated agent waves, and upstream synchronizations targeting `main` MUST satisfy every gate below with **zero warnings, zero incremental exemptions, and zero skipped tests**.
> This internal governance specification is marked `export-ignore` in `.gitattributes` and excluded from customer source archives by `scripts/export-source.sh`. Always use `<repo-root>` placeholders in documentation and audit ledgers — never commit host-specific filesystem paths.

---

## 1. Dimension 1 — Bug Prevention, Runtime Reliability, Transactions & Dual-DB Parity

### 1.1 Mandatory TDD Defect-Closure Loop
- Every `P0`–`P3` bug fix MUST follow a strict **Red -> Green -> Verify** loop:
  1. Add a deterministic failing regression unit test reproducing the defect or boundary violation.
  2. Implement the minimal root-cause fix without introducing side-effect workarounds.
  3. Confirm the regression test and full package test suite (including `-race` for Go concurrency paths) exit `0`.
- Introducing `t.Skip`, `pytest.skip`, or `.skip` to bypass failing or unimplemented checks is strictly forbidden.

### 1.2 State Machine Completeness & Defensive Boundaries
- Explicitly guard all state transitions across:
  - **Knowledge Pipeline**: upload, document parsing, chunking, embedding, hybrid indexing, tag/folder mutations, and failure recovery (`pending_subtasks_count`, dead-letter handling).
  - **Agent & HITL Runtime**: tool-call loops, `hitl.Broker` prompt lifecycles (`pending` / `answered` / `suspended` / `expired` / `done`), single-use approval/grant consumption, session vs. standing grants, and skill installation/rollout.
  - **IM Channels & Sandboxes**: per-channel session mode isolation, Docker & local sandbox allocation/teardown, timeout process-group reaping, and credential/env scrubbing.
  - **Multi-Tenant & Organization Isolation**: every query, cache key, stream/Redis key, storage key, and background job payload MUST bind and verify non-zero `tenant_id` (and `user_id` / RBAC role / organization share scope where applicable) and fail closed on missing identity.
- Guard all external/user inputs against nil dereferences, slice/index out-of-bounds, division-by-zero, integer overflow, and malformed UTF-8.

### 1.3 Error Handling & Transaction Atomicity
- **Zero Silent Error Swallowing**: never discard actionable errors via `_ = ...` on DB writes, transaction `Commit`, stream/file `Flush`/`Close` on write paths, cache invalidations, or security policy checks.
- **Contextual Wrapping**: wrap lower-layer errors with `fmt.Errorf("<operation>: %w", err)` or map them explicitly to typed domain errors in `internal/errors` so HTTP/gRPC status codes and error envelopes remain deterministic.
- **Compound Mutation Safety**:
  - Multi-table relational mutations MUST execute inside `db.WithContext(ctx).Transaction(...)`.
  - Cross-store workflows (`DB + Object Storage` or `DB + External Vector/Graph Store`) MUST avoid holding open SQL transactions across slow network RPCs and MUST implement deterministic compensating cleanup or idempotent reconciliation so partial failures never leave orphan blobs, vectors, or dangling DB rows.

### 1.4 Concurrency & Resource-Closure Discipline
- Every background goroutine MUST accept a `context.Context`, respect `ctx.Done()`, and participate in deterministic shutdown (`sync.WaitGroup` / lifecycle `Close()`).
- Never use `time.After` inside loops or high-throughput select paths; use `time.NewTimer` / `time.NewTicker` with `defer Stop()`.
- Every acquired I/O or database handle MUST be deterministically closed via `defer`:
  - `http.Response.Body` (bounded drain + `Close()`), `*sql.Rows` (plus `rows.Err()` check), `*sql.Stmt`, OS files/tempdirs, gRPC streams/connections, and SSE/WebSocket streams.
- Untrusted network, archive, and upload streams MUST enforce explicit byte/entry ceilings via `io.LimitReader` or `http.MaxBytesReader`.

### 1.5 Dual-DB (`PostgreSQL / ParadeDB` + `SQLite Lite`) Semantic & Migration Parity
- `GENYEE` supports exactly two primary relational drivers (`DB_DRIVER=postgres` and `DB_DRIVER=sqlite`; `go-sql-driver/mysql` is retained solely as the wire driver for Apache Doris retrieval).
- All GORM queries and raw SQL MUST maintain identical runtime semantics across PostgreSQL (`migrations/versioned/`) and SQLite (`migrations/sqlite/` + `sqlite-vec`):
  - Avoid unguarded dialect-specific operators (`ILIKE`, Postgres-only JSONB path operators, raw array literals, or incompatible date/window expressions) without portable abstraction or tested dual-driver branches.
  - Eliminate N+1 query loops in list/tree/hydration paths via batched `IN` queries or joins, backed by composite indexes with `tenant_id` leading on tenant-scoped tables.
- Every schema change MUST atomically ship:
  1. Sequential reversible PostgreSQL migrations (`migrations/versioned/NNNNNN_<name>.up.sql` and `.down.sql`, continuing sequentially from `000140_*`).
  2. Sequential reversible SQLite migrations (`migrations/sqlite/NNNNNN_<name>.up.sql` and `.down.sql`, continuing sequentially from `000056_*`).
  3. Synchronized updates to `migrations/paradedb/00-init-db.sql`, `website-docs/06-development/02-database-schema.md`, and `internal/database/` migration parity tests.

---

## 2. Dimension 2 — Clean Architecture, Layering Guards & Modularity

### 2.1 Strict Unidirectional Layering
- Backend packages MUST obey strict inward dependency direction:
  `HTTP Handler (internal/handler) -> Application Service / Domain Interface (internal/application/service, internal/types/interfaces) -> Repository / Infrastructure (internal/application/repository, internal/infrastructure)`
- **Hard Architectural Prohibitions** (enforced by Go AST unit test `internal/architecture_test.go`):
  1. Non-test files in `internal/handler/...` MUST NEVER import `internal/application/repository/...` concrete packages.
  2. Non-test files in `internal/handler/...` MUST NEVER hold, accept, or query `*gorm.DB` directly.
- Cross-cutting concerns MUST use canonical internal packages:
  - Unified HTTP response helpers and `internal/errors` domain-to-HTTP mapping.
  - End-to-end `context.Context` propagation from Gin/gRPC/Asynq entrypoints down to every repository, model, and storage call.
  - Structured, redacted logging via `internal/logger` — **never** log plaintext API keys, bearer tokens, cookies, passwords, signing keys, DSNs, or raw user secrets, and mask secret-looking environment variables before serializing prompts to external models.

### 2.2 Single-File Modularity Ceiling (<= 2,500 Lines)
- Multi-responsibility source or component files exceeding **2,500 lines** are prohibited across Go, TypeScript, Vue, and Python:
  - **Frontend Vue/TS**: decompose large views and modals into cohesive domain sub-components (`PascalCase.vue`) paired with focused typed composables (`use*.ts`) and pure helper modules.
  - **Backend Go**: split oversized service/domain files by cohesive responsibility (e.g., parsing, chunking, embedding, graph extraction, recovery) within the package rather than accumulating monolithic files.

### 2.3 Zero Orphan Assets & Dead Code
- Unwired historical directories (such as the removed `migrations/mysql/`), unreferenced root demo/test scripts, unused exported symbols, and commented-out legacy code blocks MUST NOT accumulate in the repository.

---

## 3. Dimension 3 — Full-Stack Zero-Warning Static Analysis & Formatting Gates

Incremental lint exemptions (`--new-from-merge-base`, `--new-from-rev`, path-filtered debt skips) are prohibited in CI and release gates. Every stack enforces **zero errors and zero warnings** across 100% of tracked code:

| Stack | Scope | Mandatory Tools & Configuration |
| :--- | :--- | :--- |
| **Go (`go1.27+`)** | Root module `.`, `cli/`, `client/`, `third_party/anydoc-go/`, `docs/poc/` | `golangci-lint` v2 (`.golangci.yml`: `govet`, `errcheck`, `staticcheck`, `revive`, `bodyclose`, `sqlclosecheck`, `ineffassign`, `unused`, `gocritic`, `misspell`, `lll` at 120 columns with `docs/docs.go` and Swag/struct-tag exclusions) + `gofmt` & `gofumpt` (`0` unformatted files). |
| **Python (`3.11+`)** | `docreader/`, `mcp-server/`, `docker/odl-hybrid/`, `scripts/**/*.py`, `dataset/`, `tests/` | `ruff check` (`0` violations) + `ruff format --check` (`0` unformatted files) + `mypy` (`python_version = "3.11"`, `0` type errors). Generated `*_pb2*.py` files are excluded from hand-authored lint rules. |
| **Frontend & TS** | `frontend/` (`@genyee/frontend`), `packages/dsh-genyee/`, `website-docs/` (incl. `homepage/`) | ESLint Flat Config (Vue 3 + strict TypeScript rules, `0` warnings/errors) + `vue-tsc --build` / `tsc --noEmit` (`0` errors). |
| **Shell, Docker & Helm** | `scripts/**/*.sh`, `docker/**/*.sh`, `frontend/docker-entrypoint.sh`, `helm/` | `shellcheck` (`0` findings); `helm lint ./helm` (`0` warnings); 100% env-key alignment across `.env.example`, `.env.lite.example`, `docker-compose.yml`, `docker-compose.dev.yml`, `helm/values.yaml`, and `internal/config`. |

---

## 4. Dimension 4 — Zero-Legacy Contract Naming & Multi-End Lockstep Sync

### 4.1 Canonical Naming & Zero Legacy Fallbacks
- Maintain clean, unambiguous naming with **zero legacy compatibility aliases or dual-write shims**:
  - **HTTP Routes**: RESTful `/api/v1/...` kebab-case plural resource nouns, consistent path parameter names, and accurate HTTP verbs/status codes.
  - **JSON DTOs**: consistent casing per API contract surface, explicit validation tags, and `DisallowUnknownFields` on security-critical control payloads.
  - **Database Schema**: `snake_case` tables, columns, and deterministic index/constraint names (`idx_<table>_<cols>`, `uq_<table>_<cols>`).
  - **Environment & Config Keys**: canonical `GENYEE_*` prefix for project-level runtime settings and `--gy-*` / `gy-*` for UI tokens/prefixes.

### 4.2 Six-End Contract Lockstep
- Any change to an HTTP route, request/response DTO, SSE event payload, or auth/header requirement MUST be updated atomically across all six surfaces in the same wave:
  1. **Gin Router & Handlers** (`internal/router/`, `internal/handler/`)
  2. **Generated OpenAPI Specs** (`swag init` via `make docs` -> `docs/{docs.go,swagger.json,swagger.yaml}`, containing zero `github_com_Tencent_` definitions)
  3. **Web Frontend API Layer** (`frontend/src/api/`)
  4. **Go CLI** (`cli/`)
  5. **Go Client SDK** (`client/`)
  6. **Python MCP Server & Product Docs** (`mcp-server/`, `website-docs/`)
- Contract alignment MUST be verified by `docs/swagger_contract_test.go` and cross-client contract test suites.

---

## 5. Dimension 5 — Modern Web Platform (`modern-web-guidance`), Tokens, Strict Types & 6-Locale i18n

All `frontend/` and `website-docs/` engineering follows [GoogleChrome/modern-web-guidance](https://github.com/googlechrome/modern-web-guidance) alongside `GENYEE` design-system invariants:

### 5.1 Native Web APIs & Rendering Performance
- Prefer baseline native Web APIs over ad-hoc manual utilities or legacy polyfills:
  - `structuredClone` for deep cloning plain state payloads.
  - `AbortController` / `AbortSignal.timeout` for cancellable `fetch`/SSE requests and component `onUnmounted` cleanup.
  - `URL` / `URLSearchParams` for query/path construction (never string-concatenated unescaped URLs).
  - `ResizeObserver`, `IntersectionObserver`, and `requestAnimationFrame` with deterministic teardown on unmount.
- Keep `vite.config.ts` chunk splitting minimal and deterministic; avoid deep reactive watchers on large immutable trees (`shallowRef` where appropriate).

### 5.2 Modern CSS & `--gy-*` Light/Dark Design Tokens
- All colors, surfaces, borders, shadows, and semantic states MUST consume `--gy-*` CSS custom properties with verified light and dark theme parity (`frontend/src/assets/theme/theme.css`).
- Hardcoded `#hex` / `rgb()` / `rgba()` color literals in view/component styles (outside canonical token definitions and intentional brand illustrations) and legacy `--wk-*` prefixes are prohibited.
- Prefer modern CSS layout and sizing (`gap`, logical properties, `clamp()`, `color-mix()`, `:focus-visible`, container/media queries) over brittle pixel hacks.

### 5.3 Semantic HTML, Forms & Accessibility (a11y)
- Interactive controls MUST use semantic elements (`<button type="button">`, `<a href>`, `<nav>`, `<main>`, `<section>`, `<form>`, `<label>`) rather than click-only `<div>`/`<span>` elements lacking keyboard/ARIA support.
- Ensure keyboard operability (`Tab`, `Enter`/`Space`, `Escape`), `:focus-visible` outlines, explicit `aria-label` on icon-only buttons, and compatibility with TDesign Vue Next accessibility props.

### 5.4 Strict TypeScript (`0 any`) & File Naming Conventions
- **Zero Explicit `any`**: non-test `.ts`, `.tsx`, and `.vue` files under `frontend/src/`, `packages/dsh-genyee/`, and `website-docs/` MUST contain **0** explicit `any` annotations (`: any`, `as any`, `<any>`, `any[]`). Define accurate domain interfaces, discriminated unions, or `unknown` with type narrowing.
- **Canonical File Naming**:
  - `frontend/src/components/`: `PascalCase.vue` (e.g., `InputField.vue`).
  - `frontend/src/api/`: `kebab-case.ts` or `<module>/index.ts`.
  - Composables: `use<DomainName>.ts`.

### 5.5 Six-Locale Internationalization (`0` Bare CJK)
- All user-visible copy in non-test `frontend/src/` source MUST go through `vue-i18n` with **zero bare Chinese/CJK UI literals** in `.vue`/`.ts` source files (enforced by automated source guard).
- Every locale key MUST maintain 100% structural and placeholder parity across all 6 supported locales: `zh-CN`, `en-US`, `ja-JP`, `ko-KR`, `ru-RU`, and `zh-TW` (verified by `frontend/src/i18n/localeKeyAudit.test.ts`).

---

## 6. Host Resource Discipline (7.8 GiB RAM), Compliance Red Lines & Branch Topology

### 6.1 7.8 GiB RAM Anti-OOM Execution Discipline
1. **Containers**: keep `GENYEE-*` runtime Docker containers stopped during development and verification. Only launch a single ephemeral `paradedb/paradedb:v0.25.11-pg17` container when executing live PostgreSQL migration gates, and destroy it immediately afterward (`docker rm -f`).
2. **Go Toolchain**: every `go build`, `go vet`, `go test`, `go test -race`, and `golangci-lint` command MUST run serially with `GOMAXPROCS=2` and `-p 1` (and `-o /dev/null` when compiling standalone `main` packages such as `docs/poc/...`).
3. **Python Toolchain**: always set `PYTHONDONTWRITEBYTECODE=1` and pass `-p no:cacheprovider` to `pytest`; run `docreader` and `mcp-server` test suites sequentially.
4. **Frontend Toolchain**: run `vue-tsc --build` (`NODE_OPTIONS="--max-old-space-size=2560"`) and `vite build` (`NODE_OPTIONS="--max-old-space-size=3072"`) inside isolated `/tmp` copies so build artifacts never pollute the working tree, and remove `/tmp` directories upon completion.
5. **Parallel Worktrees**: limit concurrent `git worktree` / `share` workers to at most 3–4 per wave, staggering heavy Go `-race` and frontend builds.

### 6.2 MIT Statutory Attribution & Commercial Compliance Iron Law
- **Zero-Byte Modification Red Line**: the following legal, attribution, and vendored notice files are strictly protected and MUST maintain a **0-byte diff** unless explicitly authorized by dedicated legal review:
  - `LICENSE`, `frontend/LICENSE`, `website-docs/LICENSE`, `mcp-server/LICENSE`, `mcp-server/LICENSE.GENYEE`, `packages/dsh-genyee/LICENSE`
  - All 4 copies of `THIRD_PARTY_NOTICES.md` (root, `frontend/`, `mcp-server/`, `website-docs/`)
  - `licenses/**`, `OFFLINE_LICENSES/**`, `dataset/README`, `dataset/README_zh.md`, `third_party/anydoc-go/LICENSE`
  - Bottom `Attribution & Non-Affiliation Notice` sections in `README.md`, `README_CN.md`, `README_JA.md`, and `README_KO.md`
  - `BrowserSkill` upstream attribution and official Chrome/Edge Web Store URLs documented in `REBRAND_RESERVED.md`
  - Vendored icon sprite `frontend/public/tdesign-icons/`
- **Product & Dependency Boundaries**:
  - Prohibited copyleft or non-commercial dependencies (`github.com/shakinm/xlsReader`, `ebooklib`, `textract`, MS MARCO samples) and retired directories (`miniprogram/`, `misc/pr-assets/`, discontinued managed cloud integrations) MUST remain permanently absent, while personal WeChat (`internal/im/wechat`) and WeCom (`internal/im/wecom`) IM channels remain intact.
  - `Langfuse` tracing MUST remain opt-in and disabled by default (`LANGFUSE_ENABLED=false`, `PROFILES=""`, gated under `--profile langfuse`, with zero default `cloud.langfuse.com` endpoints).
  - Any dependency update in `go.mod` or `frontend/package.json` MUST pass `scripts/check-license-bundle.sh`, `scripts/test-license-bundle.sh`, `frontend/scripts/generate-npm-notices.mjs`, and all 34 checks in `scripts/verify-commercial-compliance.sh`.

### 6.3 Three-Branch Topology, Dual Audit Ledgers & Source Export Gate
- **Three-Branch Rule** (see `BRANCH_GUIDE.md`): local and `origin` repositories maintain ONLY `main`, `upstream` (1:1 read-only fast-forward mirror of `Tencent/WeKnora`; NEVER push to the `upstream` remote), and `archive/legacy-pure-rebrand-main` (`74434f7303bade6b9fb90c2f315721f060d6af15`, frozen historical snapshot).
- **Dual Ledger Gate**: every wave merge and upstream sync on `main` MUST keep both `test-reports/module-audit-v2/verify_ledger.py --summary --final` and `test-reports/module-audit-v3/verify_ledger.py --summary` (and `--final` at release gate) at `exit 0` with 100% `git ls-files` coverage and `0` host-path leaks.
- **Clean Customer Source Archive**: customer source tarballs MUST be produced exclusively via `scripts/export-source.sh`, verifying `0` leaks of `.git/`, `ENGINEERING_STANDARDS.md`, `BRANCH_GUIDE.md`, `REBRAND_RESERVED.md`, `test-reports/`, `migrations/mysql/`, or internal test/CI harnesses.
