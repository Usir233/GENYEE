# 书面源代码提供要约 (Written Offer for Corresponding Source Code)

**生效日期**：随本离线交付包分发之日起生效  
**有效期**：自您收到本离线交付包或设备之日起 **三（3）年**（依据 GPLv3 第 6(b) 条、AGPLv3 第 6(b) 条与第 13 条、GPLv2 第 3(b) 条及 LGPLv2.1/v3 相关条款）

---

## 一、要约声明 (Written Offer Statement)

本离线交付包（Air-Gapped Bundle）中包含若干以目标代码（容器镜像二进制层或系统软件包）形式分发的第三方自由/开源软件组件。这些组件分别受 **GNU Affero General Public License v3.0 (AGPLv3)**、**GNU General Public License v3.0 / v2.0 (GPLv3 / GPLv2)**、**GNU Lesser General Public License (LGPL)** 或 **Mozilla Public License 2.0 (MPL-2.0)** 约束。

上述全部第三方组件均按上游官方发布状态**原样（unmodified）**打包，GENYEE 未对其源代码进行任何修改，且仅将其作为独立容器或独立命令行子进程（Aggregate）运行。

在此，分发方郑重向任何收到本离线交付包的接收方作出书面承诺（Written Offer）：

> **在自本次分发之日起三（3）年内，任何接收方均可联系分发方或按下述公开/存档渠道，免费获取（或仅支付不超过实际介质复制与邮寄成本的费用）本交付包中所含全部受 GPL / AGPL / LGPL / MPL 约束组件的完整对应机器可读源代码（Corresponding Source），包括构建脚本与精确版本标识。**

---

## 二、涵盖组件与上游对应源码地址清单

### 1. 独立基础设施容器镜像

| 组件名称 | 离线包固定镜像标签 | 许可证 | 对应上游源码仓库与精确版本 Tag |
| :--- | :--- | :--- | :--- |
| **ParadeDB** (PostgreSQL + pg_search) | `paradedb/paradedb:v0.25.11-pg17` | AGPL-3.0-only | `https://github.com/paradedb/paradedb/tree/v0.25.11` |
| **MinIO** (pgsty 发行构建) | `pgsty/minio:RELEASE.2026-08-04T00-00-00Z` | AGPL-3.0-only | `https://github.com/minio/minio/tree/RELEASE.2026-08-04T00-00-00Z` |
| **SearXNG** | `searxng/searxng:2025.5.18-5286903` | AGPL-3.0-or-later | `https://github.com/searxng/searxng/commit/5286903` |
| **Neo4j Community & APOC** | `neo4j:2025.10.1` | GPL-3.0-or-later / AGPL-3.0 | `https://github.com/neo4j/neo4j/tree/2025.10.1` |
| **BusyBox** | `busybox:1.36` | GPL-2.0-only | `https://git.busybox.net/busybox/tag/?h=1_36_0` |

### 2. 容器内系统级开源软件包（`genyee-docreader` 与 `genyee-sandbox`）

`genyee-docreader` 与 `genyee-sandbox` 基于 Debian GNU/Linux 官方镜像构建，通过 `apt-get` 安装了以下未修改的系统级开源工具包（仅通过进程间 CLI 或系统动态库调用）：

| 软件包名称 | 用途说明 | 许可证 | Debian 官方源码获取方式 |
| :--- | :--- | :--- | :--- |
| **LibreOffice** (`libreoffice-writer`, `libreoffice-impress` 等) | `.doc` / `.ppt` 转换子进程 | MPL-2.0 / LGPL-3.0+ | `apt-get source libreoffice` 或 `https://sources.debian.org/src/libreoffice/` |
| **Poppler** (`poppler-utils` / `pdftoppm`) | PDF 页面渲染工具 | GPL-2.0 / GPL-3.0 | `apt-get source poppler` 或 `https://sources.debian.org/src/poppler/` |
| **Ghostscript** | PostScript / PDF 处理 | AGPL-3.0 | `apt-get source ghostscript` 或 `https://sources.debian.org/src/ghostscript/` |
| **FFmpeg** (`ffmpeg`) | 音视频格式转码 CLI | LGPL-2.1+ / GPL-2.0+ | `apt-get source ffmpeg` 或 `https://sources.debian.org/src/ffmpeg/` |
| **GNU Coreutils / Bash / Glibc** | 基础容器运行环境 | GPL-3.0+ / LGPL-2.1+ | `https://snapshot.debian.org/` |

---

## 三、如何查验与索取对应源代码 (How to Obtain Corresponding Source)

1. **核对镜像精确摘要（Image Digest）**：
   查看本离线交付包根目录下的 `MANIFEST.json` 与 `SHA256SUMS`，其中记录了每个导出镜像归档的 SHA-256 校验值及 OCI RepoDigest。
2. **获取容器内安装的 Debian 软件包精确版本清单**：
   可在加载镜像后执行以下命令导出精确包版本，并通过 `https://snapshot.debian.org/` 或 `apt-get source <package>=<version>` 获取对应源码包（`.dsc`、`.orig.tar.*`、`.debian.tar.*`）：
   ```bash
   docker run --rm --entrypoint dpkg-query genyee/genyee-docreader:latest -W -f='${Package}=${Version} (${Source})\n'
   docker run --rm --entrypoint dpkg-query genyee/genyee-sandbox:latest -W -f='${Package}=${Version} (${Source})\n'
   ```
3. **向分发方申请离线源码归档介质**：
   如您处于完全断网（Air-Gapped）环境、无法访问上述上游代码仓库或 Debian Snapshot 源，请凭本交付包的 `MANIFEST.json` 联系向您提供本软件包的商业交付团队或通过项目官方仓库提交工单，我们将在收到请求后提供与本交付包镜像 Digest 完全对应的源码归档包（Source Tarball Bundle）。
