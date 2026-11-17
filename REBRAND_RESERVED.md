# GENYEE 镜像参数化与私有仓库 / 离线部署说明

本文档记录在将项目全量重构为 **GENYEE**（`github.com/Usir233/GENYEE`）过程中，所有自研镜像的统一参数化机制、私有镜像仓库（如阿里云 ACR / 内网 Harbor）覆盖方式，以及保留的浏览器扩展兼容项说明。

> **适用范围与三分支治理状态（2026-11-17 更新）**：商业合规改造、上游残留标识清零、全仓模块安全审计（`W0`–`W8`）及上游 `f64dbf002` / `7d064bc3a` 合并审计已全部完成并正式收敛至仓库默认主分支 `main`；全仓现严格仅保留 `main`、`upstream` 与 `archive/legacy-pure-rebrand-main` 三条分支（历史阶段分支与早期临时整改清单均已退役删除）。本文档所有镜像参数化规范与第三方 `BrowserSkill` 保留规则直接适用于 `main` 主干及后续全部上游同步（详见 [BRANCH_GUIDE.md](BRANCH_GUIDE.md)）。

---

## 一、自研 Docker 镜像与仓库前缀参数化（已完成切换）

项目中全部自研组件镜像均已由上游 `wechatopenai/weknora-*` 切换为 `genyee/genyee-*`，并通过环境变量 `GENYEE_REGISTRY`（默认 `genyee`）或 Helm `global.imageRegistry` 实现统一参数化。

### 1. 核心自研镜像与 Dockerfile 对应关系

| 组件说明 | 默认镜像名 | 对应 Dockerfile 路径 | 私有仓库 / 阿里云 ACR 目标镜像名示例 |
| :--- | :--- | :--- | :--- |
| **前端 Web UI** | `${GENYEE_REGISTRY:-genyee}/genyee-ui:${GENYEE_VERSION:-latest}` | `frontend/Dockerfile` | `registry.cn-hangzhou.aliyuncs.com/<命名空间>/genyee-ui:latest` |
| **后端主程序 (Go)** | `${GENYEE_REGISTRY:-genyee}/genyee-app:${GENYEE_VERSION:-latest}` | `docker/Dockerfile.app` | `registry.cn-hangzhou.aliyuncs.com/<命名空间>/genyee-app:latest` |
| **文档解析服务 (Python)** | `${GENYEE_REGISTRY:-genyee}/genyee-docreader:${GENYEE_VERSION:-latest}` | `docker/Dockerfile.docreader` | `registry.cn-hangzhou.aliyuncs.com/<命名空间>/genyee-docreader:latest` |
| **代码执行沙箱** | `${GENYEE_REGISTRY:-genyee}/genyee-sandbox:${GENYEE_VERSION:-latest}` | `docker/Dockerfile.sandbox` | `registry.cn-hangzhou.aliyuncs.com/<命名空间>/genyee-sandbox:latest` |
| **OpenDataLoader 混合解析** | `${GENYEE_REGISTRY:-genyee}/genyee-odl-hybrid:${GENYEE_VERSION:-latest}` | `docker/Dockerfile.odl-hybrid` | `registry.cn-hangzhou.aliyuncs.com/<命名空间>/genyee-odl-hybrid:latest` |
| **MCP Server** | `${GENYEE_REGISTRY:-genyee}/genyee-mcp:${GENYEE_VERSION:-latest}` | `mcp-server/Dockerfile` | `registry.cn-hangzhou.aliyuncs.com/<命名空间>/genyee-mcp:latest` |

---

### 2. 已完成参数化的文件位置清单

无需手动修改源码，即可通过环境变量或 Helm Values 切换至私有镜像仓库：

#### （1）Docker Compose 与环境变量
- **`docker-compose.yml` / `docker-compose.dev.yml`**
  - 所有自研服务镜像均使用 `${GENYEE_REGISTRY:-genyee}/genyee-*:${GENYEE_VERSION:-latest}`。
- **`.env.example`**
  - 新增 `GENYEE_REGISTRY=genyee`（可覆盖为内网 Harbor 或阿里云 ACR 前缀）。
  - 新增 `GENYEE_SANDBOX_DOCKER_IMAGE=genyee/genyee-sandbox:latest`（可覆盖 Docker 沙箱默认镜像）。

#### （2）代码内默认沙箱（Sandbox）镜像常量与环境变量覆盖
- **后端默认常量**：`internal/sandbox/sandbox.go`
  - `DefaultDockerImage` / `DefaultSandboxDockerImage = "genyee/genyee-sandbox:latest"`
  - `DefaultCubeTemplateImage = "genyee/genyee-sandbox:latest-cube"`
  - `DefaultDesktopDockerImage` / `DefaultDesktopSandboxImage = "genyee/genyee-sandbox:desktop"`
  - `DefaultCubeDesktopTemplateImage = "genyee/genyee-sandbox:desktop-cube"`
  - 支持通过环境变量 `GENYEE_SANDBOX_DOCKER_IMAGE` 动态覆盖默认沙箱镜像。
- **前端沙箱设置抽屉默认值**：`frontend/src/components/SandboxConfigEditorDrawer.vue`
  - `defaultDockerImage = 'genyee/genyee-sandbox:latest'`，且 `isStandardTemplateImage` 仅识别 `genyee/genyee-sandbox`（及私有仓库后缀 `/genyee-sandbox`），已按零向后兼容原则移除历史旧镜像兼容。
- **启动脚本默认沙箱检测**：`scripts/start_all.sh`
  - 自动读取 `${GENYEE_SANDBOX_DOCKER_IMAGE:-${GENYEE_REGISTRY:-genyee}/genyee-sandbox:${GENYEE_VERSION:-latest}}`。

#### （3）构建脚本、Makefile、Helm 与离线交付包
- **`Makefile` & `scripts/build_images.sh`**
  - 支持 `GENYEE_REGISTRY ?= genyee`，一键构建并标记 `${GENYEE_REGISTRY}/genyee-*` 镜像。
- **`helm/values.yaml` & `helm/templates/_helpers.tpl`**
  - 默认仓库已更新为 `genyee/genyee-{app,ui,docreader}`，并新增 `global.imageRegistry` 支持统一注入私有仓库前缀。
- **`scripts/offline-bundle.sh` & `scripts/offline-load.sh`**
  - 支持离线环境一键打包、SHA256 校验、加载镜像及 `--registry <registry>` 自动重打标签推送。

---

### 3. 私有镜像仓库 / 阿里云 ACR 编译与推送命令参考

假设你的私有镜像仓库前缀为 `GENYEE_REGISTRY=registry.cn-hangzhou.aliyuncs.com/your_namespace`：

```bash
# 1. 登录私有镜像仓库
docker login registry.cn-hangzhou.aliyuncs.com

# 2. 使用构建脚本一键编译全部自研镜像（自动带上 GENYEE_REGISTRY 前缀）
GENYEE_REGISTRY=registry.cn-hangzhou.aliyuncs.com/your_namespace ./scripts/build_images.sh --all

# 3. 推送镜像或使用离线打包脚本导出
docker push ${GENYEE_REGISTRY}/genyee-ui:latest
docker push ${GENYEE_REGISTRY}/genyee-app:latest
docker push ${GENYEE_REGISTRY}/genyee-docreader:latest
docker push ${GENYEE_REGISTRY}/genyee-sandbox:latest
```

---

## 二、浏览器连接插件保留项

由于目前尚未单独打包上架专属的 BrowserSkill 浏览器连接扩展，以下外部插件商店链接与通信标识已保留原状，确保现有浏览器连接功能可直接使用：

### 1. 本地浏览器连接扩展商店地址
- **`frontend/src/views/settings/BrowserConnectionSettings.vue`**
  - 第 `188-191` 行（BrowserSkill 本地浏览器连接插件的 Chrome / Edge 商店安装地址）：
    ```ts
    const EXTENSION_STORES = [
      { label: 'localBrowser.storeInstall', url: 'https://chromewebstore.google.com/detail/hhcmgoofomhgciiibhipgmgkgnoenaoi' },
      { label: 'localBrowser.edgeStoreInstall', url: 'https://microsoftedge.microsoft.com/addons/detail/browserskill/emacgiaaaiojkkpkddmmdfhmokgmnikg' },
    ]
    ```
  - 第 `16`、`29`、`174` 行：BrowserSkill 插件图标（`@/assets/browserskill/logo.png`）及上游说明链接（`https://github.com/Tencent/BrowserSkill`）保留原状。

### 2. 环境变量命名规范说明
- `docker-compose.yml` 和 `.env.example` 中的项目级环境变量名（如 `GENYEE_VERSION`、`GENYEE_REGISTRY`、`GENYEE_SANDBOX_DOCKER_IMAGE` 等）已统一规范为 `GENYEE_*` 前缀，不保留旧品牌前缀兼容。
