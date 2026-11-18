#!/usr/bin/env python3
"""
GENYEE MCP Server 启动脚本

注意：在 stdio 传输下，stdout 是 JSON-RPC 通道，所有诊断/提示信息必须写入
stderr，否则会破坏 MCP 协议流导致客户端判定"启动失败"。本脚本所有 print
均通过 stderr 输出。
"""

import argparse
import asyncio
import os
import sys
from collections.abc import Sequence
from pathlib import Path


def ensure_module_path() -> str:
    """Ensure the mcp-server directory is on sys.path for direct script invocation."""
    module_dir = str(Path(__file__).resolve().parent)
    if module_dir not in sys.path:
        sys.path.insert(0, module_dir)
    return module_dir


ensure_module_path()


def _normalize_transport(value: str) -> str:
    cleaned = value.strip().lower()
    if cleaned == "streamable-http":
        return "http"
    return cleaned


def _default_mcp_transport(default: str = "stdio") -> str:
    raw = os.getenv("MCP_TRANSPORT")
    if raw is None or not raw.strip():
        return default
    return _normalize_transport(raw)


def _default_mcp_port(default: int = 8000) -> int:
    raw = os.getenv("MCP_PORT")
    if raw is None or not raw.strip():
        return default
    try:
        port = int(raw.strip())
    except ValueError:
        print(f"警告: MCP_PORT 无效，已回退到默认端口 {default}", file=sys.stderr)
        return default
    if not (1 <= port <= 65535):
        print(
            f"警告: MCP_PORT 超出范围 (1-65535)，已回退到默认端口 {default}",
            file=sys.stderr,
        )
        return default
    return port


def parse_arguments(argv: Sequence[str] | None = None) -> argparse.Namespace:
    """解析命令行参数（与 main.py 传输标志保持一致）"""
    parser = argparse.ArgumentParser(
        description="GENYEE MCP Server - Model Context Protocol server for GENYEE API"
    )
    parser.add_argument("--check-only", action="store_true", help="仅检查环境配置，不启动服务器")
    parser.add_argument("--verbose", "-v", action="store_true", help="启用详细日志输出")
    parser.add_argument("--version", action="version", version="GENYEE MCP Server 1.1.1")
    parser.add_argument(
        "--transport",
        type=_normalize_transport,
        choices=["stdio", "sse", "http"],
        default=_default_mcp_transport("stdio"),
        help="Transport type: stdio (default), sse, or http",
    )
    parser.add_argument(
        "--host",
        default=os.getenv("MCP_HOST", "127.0.0.1"),
        help="Bind host for network transports (default: 127.0.0.1)",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=_default_mcp_port(8000),
        help="Bind port for network transports (default: 8000)",
    )
    return parser.parse_args(argv)


def check_environment() -> None:
    """检查环境配置"""
    base_url = os.getenv("GENYEE_BASE_URL")
    api_key = os.getenv("GENYEE_API_KEY")

    if not base_url:
        print(
            "警告: GENYEE_BASE_URL 环境变量未设置，使用默认值: http://localhost:8080/api/v1",
            file=sys.stderr,
        )

    if not api_key:
        print("警告: GENYEE_API_KEY 环境变量未设置", file=sys.stderr)

    print(
        f"GENYEE Base URL: {base_url or 'http://localhost:8080/api/v1'}",
        file=sys.stderr,
    )
    print(f"API Key: {'已设置' if api_key else '未设置'}", file=sys.stderr)


def main(argv: Sequence[str] | None = None) -> None:
    """主函数"""
    ensure_module_path()
    args = parse_arguments(argv)
    print("启动 GENYEE MCP Server...", file=sys.stderr)
    check_environment()

    if args.check_only:
        print("环境检查完成。", file=sys.stderr)
        return

    if args.verbose:
        import logging

        logging.basicConfig(level=logging.DEBUG)

    try:
        import genyee_mcp_server

        if args.transport == "stdio":
            asyncio.run(genyee_mcp_server.run())
        elif args.transport == "sse":
            if not (1 <= args.port <= 65535):
                raise ValueError(f"端口超出范围 (1-65535): {args.port}")
            asyncio.run(genyee_mcp_server.run_sse(args.host, args.port))
        elif args.transport == "http":
            if not (1 <= args.port <= 65535):
                raise ValueError(f"端口超出范围 (1-65535): {args.port}")
            asyncio.run(genyee_mcp_server.run_http(args.host, args.port))
    except ImportError as e:
        print(f"导入错误: {e}", file=sys.stderr)
        print("请确保已安装所有依赖: pip install -r requirements.txt", file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:
        print("\n服务器已停止", file=sys.stderr)
    except Exception as e:
        print(f"服务器运行错误: {e}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
