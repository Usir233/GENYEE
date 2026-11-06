#!/usr/bin/env python3
"""
GENYEE MCP Server 安装脚本
"""

from setuptools import setup


# 读取 README 文件
def read_readme():
    try:
        with open("README.md", "r", encoding="utf-8") as f:
            return f.read()
    except FileNotFoundError:
        return "GENYEE MCP Server - Model Context Protocol server for GENYEE API"


# 读取依赖
def read_requirements():
    try:
        with open("requirements.txt", "r", encoding="utf-8") as f:
            return [
                line.strip() for line in f if line.strip() and not line.startswith("#")
            ]
    except FileNotFoundError:
        return [
            "mcp>=2.2,<3",
            "requests>=2.34.2",
            "starlette>=1.3.1",
            "uvicorn>=0.51.0",
            "certifi>=2026.10.18",
            "cryptography>=50.0.0",
            "httpx2>=2.12.0",
            "opentelemetry-api>=1.45.1",
            "pyjwt>=2.15.0",
            "urllib3>=2.8.0",
        ]


setup(
    name="genyee-mcp",
    version="1.1.1",
    author="GENYEE Team",
    author_email="support@genyee.com",
    description="GENYEE MCP Server - Model Context Protocol server for GENYEE API",
    long_description=read_readme(),
    long_description_content_type="text/markdown",
    url="https://github.com/Usir233/GENYEE/tree/main/mcp-server",
    py_modules=["weknora_mcp_server", "upload_paths", "main", "run_server", "run", "test_module"],
    classifiers=[
        "Development Status :: 4 - Beta",
        "Intended Audience :: Developers",
        "Operating System :: OS Independent",
        "Programming Language :: Python :: 3",
        "Programming Language :: Python :: 3.10",
        "Programming Language :: Python :: 3.11",
        "Programming Language :: Python :: 3.12",
        "Topic :: Software Development :: Libraries :: Python Modules",
        "Topic :: Internet :: WWW/HTTP :: HTTP Servers",
        "Topic :: Scientific/Engineering :: Artificial Intelligence",
    ],
    python_requires=">=3.10",
    install_requires=read_requirements(),
    entry_points={
        "console_scripts": [
            "genyee-mcp-server=main:run",
            "weknora-mcp-server=main:run",
            "genyee-server=run_server:main",
            "weknora-server=run_server:main",
        ],
    },
    include_package_data=True,
    data_files=[
        ("", ["README.md", "requirements.txt", "LICENSE", "LICENSE.GENYEE", "THIRD_PARTY_NOTICES.md"]),
    ],
    keywords="mcp model-context-protocol genyee knowledge-management api-server",
)
