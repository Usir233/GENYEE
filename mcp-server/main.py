#!/usr/bin/env python3
"""
GENYEE MCP Server 主入口点

这个文件提供了一个统一的入口点来启动 GENYEE MCP 服务器。
可以通过多种方式运行：
1. python main.py
2. python -m genyee_mcp_server
3. genyee-mcp-server

注意：在 stdio 传输下，stdout 是 JSON-RPC 通道，所有诊断/提示信息必须写入
stderr，否则会破坏 MCP 协议流导致客户端判定"启动失败"。本文件所有 print
均通过 stderr 输出。
"""

import argparse
import asyncio
import os
import sys
from collections.abc import Sequence
from pathlib import Path


def setup_environment() -> None:
    """设置环境和路径"""
    current_dir = Path(__file__).parent.absolute()
    if str(current_dir) not in sys.path:
        sys.path.insert(0, str(current_dir))


def check_dependencies() -> bool:
    """检查依赖是否已安装"""
    try:
        import mcp  # noqa: F401
        import requests  # noqa: F401

        return True
    except ImportError as e:
        print(f"缺少依赖: {e}", file=sys.stderr)
        print("请运行: pip install -r requirements.txt", file=sys.stderr)
        return False


def check_environment_variables() -> bool:
    """检查环境变量配置"""
    base_url = os.getenv("GENYEE_BASE_URL")
    api_key = os.getenv("GENYEE_API_KEY")

    print("=== GENYEE MCP Server 环境检查 ===", file=sys.stderr)
    print(
        f"Base URL: {base_url or 'http://localhost:8080/api/v1 (默认)'}",
        file=sys.stderr,
    )
    print(f"API Key: {'已设置' if api_key else '未设置 (警告)'}", file=sys.stderr)

    if not base_url:
        print("提示: 可以设置 GENYEE_BASE_URL 环境变量", file=sys.stderr)

    if not api_key:
        print("警告: 建议设置 GENYEE_API_KEY 环境变量", file=sys.stderr)

    print("=" * 40, file=sys.stderr)
    return True


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
    """解析命令行参数"""
    parser = argparse.ArgumentParser(
        description="GENYEE MCP Server - Model Context Protocol server for GENYEE API",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
示例:
  python main.py                    # 使用默认配置启动
  python main.py --check-only       # 仅检查环境，不启动服务器
  python main.py --verbose          # 启用详细日志

环境变量:
  GENYEE_BASE_URL        GENYEE API 基础 URL (默认: http://localhost:8080/api/v1)
  GENYEE_API_KEY         GENYEE API 密钥
  MCP_SERVER_AUTH_TOKEN  SSE/HTTP 传输必填，客户端通过 Authorization: Bearer 传递
        """,
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


async def main(argv: Sequence[str] | None = None) -> None:
    """主函数"""
    args = parse_arguments(argv)

    setup_environment()

    if not check_dependencies():
        sys.exit(1)

    check_environment_variables()

    if args.check_only:
        print("环境检查完成。", file=sys.stderr)
        return

    if args.verbose:
        import logging

        logging.basicConfig(level=logging.DEBUG)
        print("已启用详细日志模式", file=sys.stderr)

    try:
        print(
            f"正在启动 GENYEE MCP Server (transport={args.transport})...",
            file=sys.stderr,
        )

        from genyee_mcp_server import run_http, run_sse, run_stdio

        if args.transport == "stdio":
            await run_stdio()
        elif args.transport == "sse":
            if not (1 <= args.port <= 65535):
                raise ValueError(f"端口超出范围 (1-65535): {args.port}")
            await run_sse(args.host, args.port)
        elif args.transport == "http":
            if not (1 <= args.port <= 65535):
                raise ValueError(f"端口超出范围 (1-65535): {args.port}")
            await run_http(args.host, args.port)

    except ImportError as e:
        print(f"导入错误: {e}", file=sys.stderr)
        print("请确保所有文件都在正确的位置", file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:
        print("\n服务器已停止", file=sys.stderr)
    except Exception as e:
        print(f"服务器运行错误: {e}", file=sys.stderr)
        if args.verbose:
            import traceback

            traceback.print_exc()
        sys.exit(1)


def sync_main(argv: Sequence[str] | None = None) -> None:
    """同步版本的主函数，用于 entry_points"""
    asyncio.run(main(argv))


def run() -> None:
    """entry_points 入口函数"""
    sync_main()


if __name__ == "__main__":
    asyncio.run(main())
