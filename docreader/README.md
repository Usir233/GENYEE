# DocReader Service

DocReader 是 GENYEE 项目中负责文档解析和图像几何预处理的独立 gRPC 服务。对象存储上传（MinIO / S3 / COS / OSS / 本地存储）、VLM 文字识别与云端解析引擎（如 MinerU / PaddleOCR-VL）统一由 Go `app` 服务负责；**请勿向 `docreader` 容器注入对象存储密钥或数据库凭据**，以保持最小权限隔离。

本地运行需要 Python 3.11 或更新版本。图片解析使用修复已知安全问题的 Pillow 13，不再支持 Python 3.10。使用 `uv sync --frozen` 安装已锁定依赖。

## Docker Compose 环境变量配置

`docreader` 默认采用 **Fail-Closed（未配置鉴权即拒绝启动）** 策略，必须与 `app` 服务配置相同的 `GRPC_AUTH_TOKEN`：

```yaml
docreader:
  image: ${GENYEE_REGISTRY:-genyee}/genyee-docreader:${GENYEE_VERSION:-latest}
  environment:
    - GRPC_AUTH_TOKEN=${GRPC_AUTH_TOKEN:?GRPC_AUTH_TOKEN is required}
    - MAX_FILE_SIZE_MB=${MAX_FILE_SIZE_MB:-50}
```

### 核心安全与连接配置

#### 1. `GRPC_AUTH_TOKEN`（必填）

- **说明**: `app` 与 `docreader` 之间的共享 Bearer 鉴权令牌（通过 `authorization: Bearer <token>` gRPC metadata 校验，常量时间比对）。
- **启动行为**: 未设置或为空白字符串时，`docreader` 会在启动阶段直接报错退出（`AuthConfigError`）。
- **本地单测豁免**: 仅在完全隔离的本机开发/测试环境中，可显式设置 `DOCREADER_ALLOW_UNAUTHENTICATED=true` 关闭鉴权；生产部署严禁启用。

#### 2. `GRPC_TLS_ENABLED` / `GRPC_TLS_CERT_FILE` / `GRPC_TLS_KEY_FILE` / `GRPC_TLS_CA_FILE`（可选）

- **说明**: 跨主机或零信任网络部署时启用 gRPC TLS / mTLS：
  - `GRPC_TLS_ENABLED=true` 时需提供服务端证书 `GRPC_TLS_CERT_FILE` 与私钥 `GRPC_TLS_KEY_FILE`。
  - 同时配置 `GRPC_TLS_CA_FILE` 时启用双向 mTLS 客户端证书校验。

#### 3. `MAX_FILE_SIZE_MB`

- **说明**: 允许接收的最大单文件大小（单位：MB，默认 `50`）。
- **用途**: 限制 gRPC 单次/流式请求消息上限，需与 Go `app` 侧 `MAX_FILE_SIZE_MB` 保持一致。

#### 4. `ODL_HYBRID_AUTH_TOKEN` 与 `DOCREADER_ODL_HYBRID_URL`（可选）

- **说明**: 启用 OpenDataLoader Hybrid PDF 后端时，`DOCREADER_ODL_HYBRID_URL` 指定内部服务地址，`ODL_HYBRID_AUTH_TOKEN` 提供共享鉴权令牌（由本机临时转发器注入 `Authorization` 头，不会暴露给子进程环境变量）。

## 其他可配置的环境变量

### gRPC 服务控制

- `DOCREADER_GRPC_PORT`: gRPC 服务监听端口（默认：`50051`）
- `DOCREADER_GRPC_MAX_WORKERS`: gRPC 工作线程池大小（默认：`4`）

### 解析器资源控制

- `DOCREADER_MARKITDOWN_MAX_WORKERS`: MarkItDown 解析的最大并发数（默认：`1`，设为 `0` 可关闭限流）
- `DOCREADER_MARKITDOWN_XLSX_MAX_TABLE_CELLS`: MarkItDown 解析 XLSX 时，整本工作簿累计允许展开的单元格数（默认：`200000`）。按各工作表实际存储行列的最大坐标矩形求和，包含表头、空洞、格式化空单元格和无缓存公式；不采用文件中的 `dimension` 提示。只接受部署环境中的正整数，非法值或 `0` 回落默认值，请求不能覆盖此限制。超限会明确报错并提示选择内置 `builtin` 引擎；不会截断数据或自动切换引擎。内置引擎按实际单元格稀疏读取，不受此表格展开预算限制。
- `DOCREADER_PDF_RENDER_MAX_WORKERS`: 扫描 PDF 渲染为图片的最大并发数（默认：`1`，设为 `0` 可关闭限流）
- `DOCREADER_PDF_RENDER_DPI`: 扫描 PDF 渲染 DPI（默认：`200`）
- `DOCREADER_PDF_JPEG_QUALITY`: 扫描 PDF 输出 JPEG 质量（默认：`85`，范围会自动限制在 `1-95`）

MarkItDown XLSX 预算的 Compose 配置示例（调整后重启 `docreader`）：

```yaml
services:
  docreader:
    environment:
      GRPC_AUTH_TOKEN: ${GRPC_AUTH_TOKEN}
      DOCREADER_MARKITDOWN_XLSX_MAX_TABLE_CELLS: "200000"
```

此预算限制的是矩形展开工作量，不等同于文件字节数、ZIP 解压或合并单元格预算；已有这些限制仍然生效。调高数值会增加 MarkItDown 的内存与 Markdown 输出开销。

### 上传 HTML 的正文抽取

- `DOCREADER_HTML_MAIN_CONTENT`: 上传的 `.html` / `.htm` 文件是否只索引正文（默认：`false`）。开启后用 trafilatura 抽取正文，与 URL 导入路径一致，导航栏 / 侧栏 / 页脚不再进入索引；抽取结果过短（< 250 字符）、不足整页转换的 15%，或抽取器额外重复正文段落时自动回退整页转换。默认关闭，升级后既有行为不变。正文图片引用会保留在 Markdown 中，供 Go 侧解析；Python 侧不会下载远程图片。

### OCR / VLM 与文档几何预处理

扫描 PDF 会被渲染为 JPEG 图片后交由 Go App 调用 VLM 提取文字。DocReader 可选用 RapidOCR 为文档照片做几何预处理，返回摆正、裁剪后的图片；最终文字识别仍由 Go App 的 VLM 完成。

在知识库的「图像处理 → 智能模式 → 文档预处理」中显式选择「自动」或「总是」才会启用。默认「关闭」，未保存该设置的旧知识库也保持关闭。「总是」仍受前面的 OCR 策略限制。此功能需要 gRPC docreader；HTTP/云端 reader 使用原图。

预处理使用独立的 RPC 执行器和受限子进程，不占用普通 `Read` / `ReadStream` 的工作线程。没有可用子进程时立即返回原图，不等待推理队列。一个子进程串行复用一套模型，空闲 60 秒后退出，释放模型内存。关闭服务、请求取消或超过本地/RPC 中较短的截止时间时，终止并回收对应子进程，后续请求会创建替代进程。

| 环境变量 | 默认值 | 含义 |
|---|---:|---|
| `DOCREADER_PREPROCESS_MAX_WORKERS` | `1` | 每个 docreader 实例最多同时运行的预处理子进程 |
| `DOCREADER_PREPROCESS_TIMEOUT_SECONDS` | `30` | 单次处理上限，含启动、模型加载和编码；RPC 截止时间可以进一步缩短 |
| `DOCREADER_PREPROCESS_THREADS` | `2` | 每个 ONNX 模型的算子内线程数及 OpenCV/数值库线程设置；算子间线程数为 1 |
| `DOCREADER_PREPROCESS_MEMORY_MB` | `2048` | 每个子进程的内存预算，单位 MiB；Linux 同时设置硬地址空间上限 `RLIMIT_AS`，父进程监测 RSS，超额即终止 |
| `DOCREADER_PREPROCESS_MAX_PIXELS` | `16000000` | 解码前检查图片头的像素上限，过大的输入直接回退 |
| `DOCREADER_PREPROCESS_PROBE_EDGE` | `2000` | 仅用于检测的工作图最长边；RapidOCR 自身也把检测器限在 2000，裁切仍回原图 |
| `DOCREADER_PREPROCESS_MAX_EDGE` | `2000` | 裁切结果的最长边上限，超过才按比例缩小；不改写原图分辨率 |

所有限制必须为正数；非法值回落到默认值，`0` 不会关闭限制。编码后的输入最多为 20 MiB。未启用此功能时不启动子进程、不加载 RapidOCR 模型。

`PreprocessImageResponse.status` 会区分 `busy`、`timeout`、`cancelled`、`memory_limit`、`image_too_large`、`worker_failed` 等回退原因。Go 侧沿用原图 OCR 的降级路径。几何阈值 `GENYEE_PREPROCESS_*` 在预处理子进程启动时记录。

### 外部出网代理与 SSRF 防护

抓取远程网页时默认经过内置 SSRF 安全校验：

- `DOCREADER_EXTERNAL_HTTP_PROXY` / `DOCREADER_EXTERNAL_HTTPS_PROXY`: 外部网页抓取使用的出网代理地址（配置导出日志会自动脱敏代理凭据）。
- LibreOffice `soffice` 子进程运行在隔离沙箱与 `http://127.0.0.1:1` 阻断代理下，不继承宿主机敏感环境变量。

## 常见问题

### 1. DocReader 服务无法启动？

优先检查是否已配置非空的 `GRPC_AUTH_TOKEN`（并与 `app` 服务一致）；若开启了 `GRPC_TLS_ENABLED=true`，请确认证书与私钥文件路径可读。

### 2. 文档中的图片如何存储与访问？

`docreader` 通过 gRPC 将提取出的图片字节返回给 Go `app` 服务，由 `app` 服务统一校验像素/格式并写入对象存储或本地存储后生成受控访问链接。若图片无法显示，请检查 `app` 服务的存储配置。

### 3. 大文件解析失败？

检查 `app` 与 `docreader` 两侧的 `MAX_FILE_SIZE_MB` 是否一致且大于待上传文档体积；若导入多个大 PDF 时内存偏高，可调低 `DOCREADER_PDF_RENDER_MAX_WORKERS` 或 `DOCREADER_MARKITDOWN_MAX_WORKERS`。

## 服务健康检查

DocReader内置 gRPC Health Checking（无需携带业务 Bearer Token 即可完成探活）：

```yaml
healthcheck:
  test: ["CMD", "/bin/grpc_health_probe", "-addr=:50051"]
  interval: 30s
  timeout: 10s
  retries: 3
  start_period: 60s
```

可以通过以下命令检查服务状态：

```bash
docker ps | grep docreader
docker logs GENYEE-docreader
```
