# GENYEE 仓库分支架构与维护规范声明 (Branch Guide)

> **重要声明（AI 助手与开发者必读）**：
> 在对本仓库执行任何拉取、合并、换皮或组件清理操作前，**必须先阅读本文档**，严格区分各分支的职责边界，严禁将不同分支的用途混为一谈。

---

## 一、分支拓扑与派生流向

```text
[上游官方仓库: Tencent/WeKnora (main)]
        │
        ▼ (1:1 原样快进同步，不做任何修改)
  ┌────────────┐
  │  upstream  │  <-- 原项目纯净备份分支
  └────────────┘
        │
        ▼ (合并上游更新 + 适配 GENYEE 品牌/包名/Logo，保留全量功能)
  ┌────────────┐
  │    main    │  <-- 纯换皮主分支（仓库默认分支，未删减任何组件）
  └────────────┘
        │
        ▼ (基于 main 派生，专门剔除非可商用/专有/未自建组件)
  ┌────────────────────────────────────────┐
  │  chore/commercial-compliance-cleanup   │  <-- 商业合规精简分支（去组件分支）
  └────────────────────────────────────────┘
```

---

## 二、各分支职责定位与铁律

### 1. `upstream` 分支 —— 原项目纯净备份支
- **定位**：1:1 追踪并备份上游官方仓库 `https://github.com/Tencent/WeKnora.git` 的 `main` 分支。
- **规则**：
  - **严禁**在 `upstream` 分支上进行任何换皮、包名修改或业务代码改动。
  - 仅用于通过 `git fetch upstream` 拉取上游 `main` 分支后执行快进更新（Fast-forward）并推送到 `origin/upstream`。

### 2. `main` 分支 —— 纯换皮主分支（默认主分支）
- **定位**：基于 `upstream` 分支进行**纯品牌换皮**后的主分支（项目名 `GENYEE`、Go 模块路径 `github.com/Usir233/GENYEE`、替换品牌 Logo 与仓库地址，保留外部硬指向依赖，详见 [REBRAND_RESERVED.md](REBRAND_RESERVED.md)）。
- **规则**：
  - **`main` 是纯换皮分支，完整保留原项目的所有功能与组件**（**不做**非可商用或专有组件的裁剪删除）。
  - **口令约定**：当用户指示**“拉取上游更新，然后合并到换皮后的项目中”**时，**指的始终是将 `upstream` 合并到 `main` 分支**（`Tencent/WeKnora:main` $\rightarrow$ `upstream` $\rightarrow$ `main`），并对上游新增/修改文件补齐 `GENYEE` 包名与品牌词替换。

### 3. `chore/commercial-compliance-cleanup` 分支 —— 商业合规去组件支
- **定位**：**专门基于 `main`（纯换皮主分支）派生**的独立分支，用于在换皮基础上完成商业化合规改造、许可证风险清理与私有化交付配套。
- **本分支实施的五项核心商业合规工程**：
  1. **架构去耦与无用高风险依赖清理**：移除非可商用数据集、未使用的受限依赖及未自建专有组件，确保主模块仅保留可商用开源依赖。
  2. **可选云服务与可观测栈默认关闭（Opt-in）**：`Langfuse` 保留代码及编排能力以维持与 `main` 分支合并零冲突，但在代码、`.env.example` 与 `docker-compose*.yml` 中**默认关闭**（`LANGFUSE_ENABLED=false`、Langfuse 容器置于 `--profile langfuse` 下按需启用）。
     - **`WeKnoraCloud` 例外（2026-11-12 决策变更）**：该服务已停用，本分支**彻底删除** WeKnora Cloud 功能，不再沿用“保留代码、默认关闭”策略。今后 `main` → 本分支合并时，对 WeKnora Cloud 相关改动一律“**保持删除**”。删除范围、数据兼容与实施步骤见 [`WEKNORA_RESIDUE_REMEDIATION.md`](WEKNORA_RESIDUE_REMEDIATION.md) 第四节。
  3. **商业授权与开源声明配套**：根目录 [`LICENSE`](LICENSE) 为 GENYEE Commercial License，仅约束其声明范围内的自有材料；上游 MIT 与第三方原始授权、版权声明保留在 [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md) 和 [`licenses/`](licenses/)，不受商业授权条款限制。镜像交付配套位于 [`OFFLINE_LICENSES/`](OFFLINE_LICENSES/README.md)，含许可证矩阵与 [`WRITTEN_OFFER.md`](OFFLINE_LICENSES/WRITTEN_OFFER.md)。
  4. **上游品牌残留清理与自研镜像参数化**：清除源码、脚本、OpenAPI/Swagger、Helm Chart 及构建产物中的上游品牌残留；将全部自研镜像切换为 `${GENYEE_REGISTRY:-genyee}/genyee-*`，并固定第三方基础设施镜像版本（如 `redis:7.2-alpine` BSD-3-Clause）。
     - 源码中剩余的 `WeKnora` / `weknora` 标识（存储键、协议头、数据默认名、代码标识符等）的分类、处理规则、兼容要求与分批实施计划，统一遵循 [`WEKNORA_RESIDUE_REMEDIATION.md`](WEKNORA_RESIDUE_REMEDIATION.md)，**禁止全局查找替换**。
  5. **私有化离线交付包与纯净源码导出机制**：
     - 离线镜像交付包：使用 `./scripts/offline-bundle.sh` 导出全量固定版本镜像、`MANIFEST.json`、`SHA256SUMS` 与 `OFFLINE_LICENSES/`，客户侧通过 `./scripts/offline-load.sh --registry <private-registry>` 一键校验并导入内网仓库。
     - 客户源码交付规范：**严禁直接将包含 `.git/` 历史与内部审计文档的工作目录打包发给客户**；向客户交付源码时，**必须**运行 `./scripts/export-source.sh`（基于 `.gitattributes` 中的 `export-ignore` 规则自动剔除 `.git/`、`COMMERCIAL_COMPLIANCE_AUDIT.md`、`AUDIT_REPORT.md`、`REBRAND_RESERVED.md`、`BRANCH_GUIDE.md`、`WEKNORA_RESIDUE_REMEDIATION.md`、`test-reports/`、`.trellis/`、`.claude/`、`.github/` 等内部维护文件）。
- **规则**：
  - 所有商业合规裁剪与默认关闭策略**仅限在此分支进行**，严禁混入 `main` 分支。
  - 当 `main` 分支合并了上游新功能后，如需同步到此分支，应从更新后的 `main` 分支向此分支合并（`main` $\rightarrow$ `chore/commercial-compliance-cleanup`）。

---

## 三、标准同步与跨分支操作守则

### 1. 日常同步上游到纯换皮主分支（`upstream` $\rightarrow$ `main`）
1. 拉取上游最新 `main` 分支并更新 `upstream` 分支：
   ```bash
   git fetch upstream
   git branch -f upstream refs/remotes/upstream/main
   git push origin upstream
   ```
2. 合并 `upstream` 到 `main` 分支，并对本次上游新增/修改的文件执行换皮适配：
   - 将新增 `.go` 文件中的 `github.com/Tencent/WeKnora` 替换为 `github.com/Usir233/GENYEE`。
   - 将新增前端多语言/文档中的用户可见品牌词 `WeKnora` 替换为 `GENYEE`（注意保留外部硬指向依赖）。
   - 运行 `go build ./...`、`go vet ./...` 与 `npm run check-i18n` 验证无误后，提交并推送到 `origin/main`。

### 2. 多窗口/跨分支防冲突铁律（使用 `git worktree`）
- 如果本地主工作目录 `<repo-root>` 当前正检出在 `chore/commercial-compliance-cleanup`（或其他分支），甚至有后台任务正在修改文件：
  - **严禁**直接在 `<repo-root>` 目录下强行执行 `git checkout main`、`git stash` 或 `git merge`，以免打断或污染另一个分支的未提交工作。
  - **必须**使用独立的临时工作树（`git worktree add <临时目录> main`）在隔离目录中完成 `main` 分支的合并、换皮适配、编译测试与 `git push origin main`，完成后清理临时 `worktree`。
