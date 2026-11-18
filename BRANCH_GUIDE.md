# GENYEE 仓库分支架构与维护规范声明 (Branch Guide)

> **重要声明（AI 助手与开发者必读）**：
> 在对本仓库执行任何拉取、合并、品牌适配或合规维护操作前，**必须先阅读本文档**。本仓库全仓（本地与远端 `origin`）**严格仅保留三条分支**：`main`（唯一默认商业合规主干）、`upstream`（上游纯净只读镜像）与 `archive/legacy-pure-rebrand-main`（旧版纯换皮只读归档快照）；所有历史阶段过渡分支及早期临时审计产物均已退役清理。

---

## 一、分支拓扑与收敛状态

```text
[上游官方仓库: Tencent/WeKnora (main)]
        │
        ▼ (1:1 原样快进同步，严禁任何本地修改)
  ┌────────────┐
  │  upstream  │  <-- 上游纯净镜像分支（只读快进，已同步至 7d064bc3a）
  └────────────┘
        │
        ▼ (直接合并入主干 + GENYEE 品牌适配 + 迁移重排 + 增量安全审计 + 34/34 合规门禁)
  ┌────────────┐
  │    main    │  <-- 默认主干分支（商业合规、GENYEE 换皮与全仓安全加固统一主干）
  └────────────┘

[唯一历史只读归档分支]
- archive/legacy-pure-rebrand-main (74434f7303bade6b9fb90c2f315721f060d6af15)
  └── 早期未裁剪组件的旧版纯换皮 main 快照（永久只读归档保留，不再接收日常合入）

[已退役并清理的过渡分支说明]
- 早期商业合规改造、上游残留清零与全仓模块安全审计（W0–W8）阶段分支已全量合流至 main，并已从本地与远端彻底删除，仓库长期仅维护上述三条分支。
```

---

## 二、各分支职责定位与合规铁律

### 1. `upstream` 分支 —— 上游官方仓库纯净镜像支
- **定位**：1:1 追踪并备份上游官方仓库 `https://github.com/Tencent/WeKnora.git` 的 `main` 分支。
- **铁律**：
  - **严禁**在 `upstream` 分支上进行任何品牌替换、包名修改、组件裁剪或业务代码改动。
  - **严禁**向远程 `upstream`（`Tencent/WeKnora`）执行任何 `git push`；仅允许通过 `git fetch upstream` 获取更新后，快进本地 `upstream` 并推送至自有远端 `origin/upstream`。

### 2. `main` 分支 —— 商业合规与安全加固默认主干
- **定位**：项目唯一默认主干分支（Go 模块路径 `github.com/Usir233/GENYEE`、统一 `GENYEE_*` / `genyee_*` / `--gy-*` / `gy-*` 命名体系），集成了商业许可证治理、上游标识清零、全仓 301 个模块单元安全审计加固（`W0`–`W8`）及私有化离线/源码交付体系。
- **`main` 主干长期固化的核心产品与合规边界**：
  1. **永久移除项（上游同步时一律保持删除）**：
     - **微信小程序客户端（`miniprogram/`）**：已彻底移除小程序客户端目录、测试与文档；**但完整保留**个人微信（`internal/im/wechat`）与企业微信（`internal/im/wecom`）IM 机器人渠道能力。
     - **已停用云服务（`WeKnoraCloud` / `GENYEECloud`）**：后端、前端、模型目录、配置开关与文档中的相关实现已彻底删除；未来合并 `upstream` 时若触及该模块，一律**保持删除**。
     - **非可商用依赖与不合规素材**：禁止引入 `shakinm/xlsReader`（GPL-3.0）、`ebooklib`（AGPL-3.0）、`textract`、MS MARCO 非商用样本、上游宣传视频或第三方客户徽标素材（如 `misc/pr-assets/*`）。
  2. **可选可观测组件默认关闭（Opt-in）**：
     - **`Langfuse`**：保留代码与编排支持，但在代码、`.env.example`、`.env.lite.example` 与 `docker-compose*.yml` 中**强制默认关闭**（`LANGFUSE_ENABLED=false`、`PROFILES=""`、容器置于 `--profile langfuse` 下，且严禁默认指向 `cloud.langfuse.com`）。
  3. **MIT 法定署名与第三方归属保护铁律（零字节篡改红线）**：
     - 根目录 [`LICENSE`](LICENSE) 为 GENYEE Commercial License（约束自有商业化材料），`frontend/LICENSE`、`website-docs/LICENSE`、`mcp-server/LICENSE.GENYEE`、`mcp-server/LICENSE`、全部 4 份 [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md)、[`licenses/`](licenses/)、[`OFFLINE_LICENSES/`](OFFLINE_LICENSES/README.md)、`dataset/README*`、`third_party/anydoc-go/LICENSE` 以及 `README*.md` 底部的 “Attribution & Non-Affiliation Notice” 法定声明受严格保护，未经专项法务与合规复核**严禁删改**。
     - 第三方开源浏览器扩展 `BrowserSkill` 的上游仓库署名与 Chrome/Edge 官方商店链接按 [`REBRAND_RESERVED.md`](REBRAND_RESERVED.md) 原样保留。
  4. **自研镜像参数化与私有化交付规范**：
     - 全部自研镜像统一采用 `${GENYEE_REGISTRY:-genyee}/genyee-*` 参数化命名（详见 [`REBRAND_RESERVED.md`](REBRAND_RESERVED.md)），Helm 锁定第三方基础镜像版本（如 `redis:7.2-alpine`）。
     - 离线镜像交付统一使用 `./scripts/offline-bundle.sh` 与 `./scripts/offline-load.sh`。
     - **客户源码包导出铁律**：向客户交付源码时，**严禁**直接打包包含 `.git/` 历史与内部维护文档的工作目录，**必须**执行 `./scripts/export-source.sh`（依托 `.gitattributes` `export-ignore` 与门禁校验自动剥离 `.git/`、`BRANCH_GUIDE.md`、`ENGINEERING_STANDARDS.md`、`REBRAND_RESERVED.md`、`test-reports/`、`tests/{adversarial,e2e,stress}`、`misc/pr-assets/` 等内部治理与测试文件）。

### 3. `archive/legacy-pure-rebrand-main` 分支 —— 旧版纯换皮归档支
- **定位**：固定指向历史提交 `74434f7303bade6b9fb90c2f315721f060d6af15`（主干晋升前未执行组件裁剪与深度残留清零的旧版纯换皮快照），仅作历史对照归档，**严禁**向其合入新代码或将其重新设为默认分支。

---

## 三、上游更新同步标准作业流程（`upstream` $\rightarrow$ `main`）

当需要将上游 `Tencent/WeKnora` 的新提交同步到本仓库时，**直接从 `upstream` 合并至 `main`**，严禁绕过以下门禁：

### 1. 拉取并快进更新 `upstream` 镜像分支
```bash
git fetch upstream --prune
git checkout upstream
git merge --ff-only refs/remotes/upstream/main
git push origin upstream:refs/heads/upstream
```
> 注：核对 `upstream` 工作区与 `refs/remotes/upstream/main` 完全一致，**绝不**向 `upstream` 远端推送。

### 2. 合并至 `main` 并完成品牌、迁移与安全对齐
```bash
git checkout main
git merge --no-commit --no-ff upstream
```
在提交合并前逐项完成以下对齐：
1. **品牌与契约零向后兼容替换规范**：禁止盲目全局替换破坏受保护许可证文件，对上游新增/修改的业务代码、配置、脚本、迁移与文档统一执行以下映射（不保留旧名回退或双写兼容）：
   - **Go 模块与仓库路径**：`github.com/Tencent/WeKnora` $\rightarrow$ `github.com/Usir233/GENYEE`；非署名用途的上游官网/文档外链（如 `weknora.weixin.qq.com`）统一改为配置项驱动或自有地址。
   - **品牌大小写映射**：`WeKnora` / `WEKNORA` $\rightarrow$ `GENYEE`（如 `NewReadGENYEEDocTool`、`GENYEE Lite`、`GENYEECONFLUENCETABLE`）；`Weknora` $\rightarrow$ `Genyee`（如 `X-Genyee-Signature`、`X-Genyee-Desktop-Token`）；`weknora` $\rightarrow$ `genyee`（如 `genyee_embeddings`、`builtin-genyee-assistant`、`search_genyee_docs`、`read_genyee_doc`、`genyee:*` Redis/事件键、`com.genyee.*` Docker 标签、`/genyee/...` 沙箱路径、`~/.genyee/` 本地目录）。
   - **缩写前缀映射（`wk` $\rightarrow$ `gy`）**：除 Apple 系统框架 `WKWebView` 外，所有上游缩写标识 `wk` / `wk_` / `wk-` / `--wk-` / `__wk` / `x-wk-` 统一替换为 `gy` / `gy_` / `gy-` / `--gy-` / `__gy` / `x-gy-`（如 `gy-display.sh`、`--gy-*` 样式变量）。
   - **文件名同步重命名**：上游新增文件名若含 `weknora` 或 `wk-` 前缀，必须 `git mv` 改为 `genyee` 或 `gy-` 并同步更新全部引用；API 注释变更后须通过 `swag init` 重新生成 `docs/{docs.go,swagger.json,swagger.yaml}`。
2. **已移除组件保持删除**：对触及 `miniprogram/`、`WeKnoraCloud` / `GENYEECloud`、`migrations/mysql/` 或 `misc/pr-assets/` 的上游变更一律保持删除，并确保 `internal/im/{wechat,wecom}` 与 `LANGFUSE_ENABLED=false` 策略不被回退。
3. **数据库迁移序号重排与初始化脚本对齐**：若上游新增 `migrations/sqlite/*` 或 `migrations/versioned/*` 与本仓已有自研迁移版本号冲突，必须顺延重排上游新增迁移序号，同步对齐 `.down.sql`、`migrations/paradedb/00-init-db.sql`、`website-docs/06-development/02-database-schema.md` 及 `internal/database/` 迁移测试。
4. **增量安全审计与台账同步**：对本次上游变更涉及的全部文件逐行开展多租户隔离、SSRF、注入、并发竞态与前端 XSS/生命周期安全审计，修复缺陷并同步更新 `test-reports/module-audit-v2/coverage.json`。

### 3. 全量门禁验收与推送
合并及修复提交推送到 `origin/main` 前，**必须全部通过（exit 0）**：
```bash
# 1. 模块审计台账完整性校验（100% git ls-files 覆盖、0 遗漏）
PYTHONDONTWRITEBYTECODE=1 python3 test-reports/module-audit-v2/verify_ledger.py --summary --final

# 2. Go / Python / 前端构建与测试门禁
GOMAXPROCS=2 go build -p 1 ./... && GOMAXPROCS=2 go vet -p 1 ./...
GOMAXPROCS=2 go test -p 1 -count=1 ./...
(cd frontend && npx tsx --test src/i18n/localeKeyAudit.test.ts)

# 3. 许可证包、34 项商业合规门禁与客户纯净源码导出校验
bash scripts/check-license-bundle.sh
PYTHONDONTWRITEBYTECODE=1 GOMAXPROCS=2 bash scripts/verify-commercial-compliance.sh
bash scripts/export-source.sh /tmp/genyee-export-check/genyee-source.tar.gz HEAD && rm -rf /tmp/genyee-export-check
```

### 4. 多窗口并行开发隔离守则（`git worktree`）
- 若本地主工作目录 `<repo-root>` 存在未提交改动或其他任务正在运行，**严禁**在 `<repo-root>` 直接强行切换分支或执行合并；必须使用 `git worktree add <临时目录> <分支名>` 在独立工作树中完成同步、审计与测试验证，验收通过后再合入并清理临时 `worktree`。
