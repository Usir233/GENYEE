#!/usr/bin/env python3
"""
GENYEE MCP Server Package

A Model Context Protocol server that provides access to the GENYEE knowledge management API.
"""

__version__ = "1.1.1"
__author__ = "GENYEE Team"
__description__ = "GENYEE MCP Server - Model Context Protocol server for GENYEE API"

from genyee_mcp_server import GENYEEClient, run

__all__ = ["GENYEEClient", "run"]
