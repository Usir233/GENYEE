# GENYEE 换皮保留项与后续阿里云 Docker 编译替换指南

本文档记录在将项目全量换皮为 **GENYEE**（`github.com/Usir233/GENYEE`）过程中，为了保证在未自建外部插件和镜像仓库前仍能正常拉取运行而**刻意保留未修改的硬指向配置**，以及后续使用 **阿里云容器镜像服务（ACR）** 编译部署时的替换清单。

---

## 一、Docker 镜像保留项（后续切换阿里云 ACR 必改清单）

当前项目中保留了上游预构建的 4 个核心 Docker 镜像名（`wechatopenai/weknora-*`）。注意：**必须使用你自己在阿里云编译出来的镜像，才能在容器运行时看到换皮后的 GENYEE 界面与代码**（直接 `docker pull wechatopenai/weknora-*` 拉取的是上游未换皮的旧镜像）。

### 1. 四个核心镜像与 Dockerfile 对应关系

| 组件说明 | 当前保留镜像名 | 对应 Dockerfile 路径 | 推荐阿里云 ACR 目标镜像名示例 |
| :--- | :--- | :--- | :--- |
| **前端 Web UI** | `wechatopenai/weknora-ui` | `frontend/Dockerfile` | `registry.cn-hangzhou.aliyuncs.com/<命名空间>/genyee-ui` |
| **后端主程序 (Go)** | `wechatopenai/weknora-app` | `docker/Dockerfile.app` | `registry.cn-hangzhou.aliyuncs.com/<命名空间>/genyee-app` |
| **文档解析服务 (Python)** | `wechatopenai/weknora-docreader` | `docker/Dockerfile.docreader` | `registry.cn-hangzhou.aliyuncs.com/<命名空间>/genyee-docreader` |
| **代码执行沙箱** | `wechatopenai/weknora-sandbox` | `docker/Dockerfile.sandbox` | `registry.cn-hangzhou.aliyuncs.com/<命名空间>/genyee-sandbox` |

---

### 2. 切换为阿里云镜像时需要修改的文件位置

当你使用阿里云编译好上述镜像后，按以下清单替换镜像地址即可：

#### （1）Docker Compose 部署文件（最核心）
- **`docker-compose.yml`**
  - 第 `3` 行：`image: wechatopenai/weknora-ui:${WEKNORA_VERSION:-latest}`
  - 第 `37` 行：`image: wechatopenai/weknora-app:${WEKNORA_VERSION:-latest}`
  - 第 `409` 行：`image: wechatopenai/weknora-sandbox:${WEKNORA_VERSION:-latest}`
  - 第 `421` 行：`image: wechatopenai/weknora-docreader:${WEKNORA_VERSION:-latest}`
- **`docker-compose.dev.yml`**
  - 第 `229` 行：`image: wechatopenai/weknora-sandbox:${WEKNORA_VERSION:-latest}`
  - 第 `243` 行：`image: wechatopenai/weknora-docreader:${WEKNORA_VERSION:-latest}`

#### （2）代码内硬编码的默认沙箱（Sandbox）镜像名
如果启用了 Agent 沙箱功能，前后端代码中有一处默认镜像名常量需要同步改为你的阿里云沙箱镜像：
- **后端默认常量**：`internal/sandbox/sandbox.go`
  - 第 `61` 行：`DefaultDockerImage = "wechatopenai/weknora-sandbox:main"`
  - 第 `71` 行：`DefaultCubeTemplateImage = "wechatopenai/weknora-sandbox:main-cube"`
  - 第 `77` 行：`DefaultDesktopDockerImage = "wechatopenai/weknora-sandbox:main-desktop"`
  - 第 `81` 行：`DefaultCubeDesktopTemplateImage = "wechatopenai/weknora-sandbox:main-desktop-cube"`
- **前端沙箱设置抽屉默认值**：`frontend/src/components/SandboxConfigEditorDrawer.vue`
  - 第 `843` 行：`const defaultDockerImage = 'wechatopenai/weknora-sandbox:main'`
- **启动脚本默认沙箱检测**：`scripts/start_all.sh`
  - 第 `329`、`588` 行：`local sandbox_image="wechatopenai/weknora-sandbox:${WEKNORA_VERSION:-latest}"`

#### （3）构建脚本与 Makefile（本地/CI 编译脚本）
- **`Makefile`**
  - 第 `80` 行：`DOCKER_IMAGE=wechatopenai/weknora-app`
  - 第 `151` 行：`docker build --platform $(PLATFORM) -f docker/Dockerfile.docreader -t wechatopenai/weknora-docreader:latest .`
  - 第 `158` 行：`-f frontend/Dockerfile -t wechatopenai/weknora-ui:latest frontend/`
- **`scripts/build_images.sh`**
  - 第 `151`、`177`、`204`、`229`~`282`、`361`~`383` 行：脚本内打包和清理镜像的 `-t wechatopenai/weknora-*` 标签。
- **`helm/values.yaml`**（仅在使用 Kubernetes Helm 部署时需关注）
  - 第 `79`、`175`、`224` 行：`repository: wechatopenai/weknora-*`

---

### 3. 阿里云 ACR 编译与推送命令参考

假设你的阿里云镜像仓库前缀为 `REGISTRY=registry.cn-hangzhou.aliyuncs.com/your_namespace`：

```bash
# 1. 登录阿里云容器镜像服务
docker login --username=<你的阿里云账号> registry.cn-hangzhou.aliyuncs.com

# 2. 编译并推送前端镜像 (genyee-ui)
docker build -f frontend/Dockerfile -t ${REGISTRY}/genyee-ui:latest frontend/
docker push ${REGISTRY}/genyee-ui:latest

# 3. 编译并推送后端主程序镜像 (genyee-app)
docker build -f docker/Dockerfile.app -t ${REGISTRY}/genyee-app:latest .
docker push ${REGISTRY}/genyee-app:latest

# 4. 编译并推送文档解析镜像 (genyee-docreader)
docker build -f docker/Dockerfile.docreader -t ${REGISTRY}/genyee-docreader:latest .
docker push ${REGISTRY}/genyee-docreader:latest

# 5. 编译并推送沙箱镜像 (genyee-sandbox，按需构建)
docker build -f docker/Dockerfile.sandbox --target sandbox -t ${REGISTRY}/genyee-sandbox:latest -t ${REGISTRY}/genyee-sandbox:main .
docker push ${REGISTRY}/genyee-sandbox:latest
docker push ${REGISTRY}/genyee-sandbox:main
```

> **提示**：你也可以直接在 `docker-compose.yml` 中使用 `docker compose build` 在阿里云 ECS 服务器上直接本地编译全部镜像并启动（`docker compose up -d --build`）。

---

## 二、浏览器抓取插件与第三方扩展保留项

由于目前尚未单独打包上架专属的 Chrome/Edge 浏览器扩展和 ClawHub 技能包，以下外部插件商店链接与通信标识已保留原状，确保现有网页抓取与浏览器连接功能可直接使用：

### 1. 网页剪藏 / 浏览器连接扩展商店地址
- **`frontend/src/config/integrations.ts`**
  - 第 `3-4` 行：
    ```ts
    export const CHROME_EXTENSION_URL =
      'https://chromewebstore.google.com/detail/jpemjbopikggjlmikmclgbmkhhopjdgd?utm_source=item-share-cb'
    ```
- **`frontend/src/views/settings/BrowserConnectionSettings.vue`**
  - 第 `188-191` 行（BrowserSkill 本地浏览器连接插件的 Chrome / Edge 商店安装地址）：
    ```ts
    const EXTENSION_STORES = [
      { label: 'localBrowser.storeInstall', url: 'https://chromewebstore.google.com/detail/hhcmgoofomhgciiibhipgmgkgnoenaoi' },
      { label: 'localBrowser.edgeStoreInstall', url: 'https://microsoftedge.microsoft.com/addons/detail/browserskill/emacgiaaaiojkkpkddmmdfhmokgmnikg' },
    ]
    ```
  - 第 `82`、`89` 行：BrowserSkill 插件图标（`browserskill-logo.svg`）及上游说明链接保留原状。

### 2. 第三方 ClawHub 技能包地址
- **`frontend/src/config/integrations.ts`**
  - 第 `6` 行：
    ```ts
    export const CLAWHUB_SKILL_URL = 'https://clawhub.ai/lyingbug/weknora'
    ```

### 3. 环境变量与兼容标识保留说明
- 为保证升级平滑与脚本兼容，`docker-compose.yml` 和 `.env.example` 中的环境变量名（如 `WEKNORA_VERSION`、`WEKNORA_API_KEY` 等内部配置键名）以及 CLI 配置文件回退读取逻辑予以保留，不会影响用户侧展示的 **GENYEE** 品牌。
