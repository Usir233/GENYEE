#!/usr/bin/env python3
"""
GENYEE MCP Server 便捷启动脚本

这是一个简化的启动脚本，提供最基本的功能。
对于更多选项，请使用 main.py

注意：在 stdio 传输下，stdout 是 JSON-RPC 通道，所有诊断/提示信息必须写入
stderr，否则会破坏 MCP 协议流导致客户端判定"启动失败"。本脚本所有 print
均通过 stderr 输出。
"""

import os
import sys
from pathlib import Path


def main() -> None:
    """简单的启动函数"""
    current_dir = Path(__file__).parent.absolute()
    if str(current_dir) not in sys.path:
        sys.path.insert(0, str(current_dir))

    base_url = os.getenv("GENYEE_BASE_URL") or "http://localhost:8080/api/v1"
    api_key = os.getenv("GENYEE_API_KEY") or ""

    print("GENYEE MCP Server", file=sys.stderr)
    print(f"Base URL: {base_url}", file=sys.stderr)
    print(f"API Key: {'已设置' if api_key else '未设置'}", file=sys.stderr)
    print("-" * 40, file=sys.stderr)

    try:
        from main import sync_main

        sync_main()
    except ImportError:
        print("错误: 无法导入必要模块", file=sys.stderr)
        print("请确保运行: pip install -r requirements.txt", file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:
        print("\n服务器已停止", file=sys.stderr)
    except Exception as e:
        print(f"错误: {e}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
