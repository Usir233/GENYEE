.PHONY: proto build run clean

# 生成 protobuf 代码
proto:
	@echo "Generating protobuf code..."
	@sh ./scripts/generate_proto.sh

# 构建 Go 客户端与 proto 包
build:
	@echo "Building Go docreader client and proto packages..."
	@cd .. && go build ./docreader/client ./docreader/proto

# 运行 Python 服务
run:
	@echo "Running Python docreader server..."
	@cd .. && uv run --project docreader python -m docreader.main

# 清理
clean:
	@echo "Cleaning up..."
	@rm -rf bin/
	@find . -name "*.pyc" -delete
	@find . -name "__pycache__" -delete
