#!/bin/sh
set -ex

# Resolve directories relative to this script so invocation works from any CWD.
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
PROTO_DIR="${PROTO_DIR:-${REPO_ROOT}/docreader/proto}"
PYTHON_OUT="${PYTHON_OUT:-${PROTO_DIR}}"
GO_OUT="${GO_OUT:-${PROTO_DIR}}"

# 生成Python代码
python3 -m grpc_tools.protoc -I"${PROTO_DIR}" \
    --python_out="${PYTHON_OUT}" \
    --pyi_out="${PYTHON_OUT}" \
    --grpc_python_out="${PYTHON_OUT}" \
    "${PROTO_DIR}/docreader.proto"

# 生成Go代码（仅在 protoc 与 Go 插件均可用时执行）
if command -v protoc >/dev/null 2>&1 && \
   command -v protoc-gen-go >/dev/null 2>&1 && \
   command -v protoc-gen-go-grpc >/dev/null 2>&1; then
    protoc -I"${PROTO_DIR}" --go_out="${GO_OUT}" \
        --go_opt=paths=source_relative \
        --go-grpc_out="${GO_OUT}" \
        --go-grpc_opt=paths=source_relative \
        "${PROTO_DIR}/docreader.proto"
else
    echo "protoc or Go protobuf plugins not found, skipping Go code generation"
fi

# 修复Python导入问题（macOS 兼容版本）
if [ "$(uname)" = "Darwin" ]; then
    sed -i '' 's/^import docreader_pb2/from docreader.proto import docreader_pb2/g' "${PYTHON_OUT}/docreader_pb2_grpc.py"
else
    sed -i 's/^import docreader_pb2/from docreader.proto import docreader_pb2/g' "${PYTHON_OUT}/docreader_pb2_grpc.py"
fi

echo "Proto files generated successfully!"
