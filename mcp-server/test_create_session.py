import asyncio
import unittest
from unittest import mock

import genyee_mcp_server as srv


class CreateSessionTest(unittest.TestCase):
    def test_mcp_schema_exposes_only_title_and_description(self) -> None:
        tools = asyncio.run(srv.mcp.list_tools())
        tool = next(tool for tool in tools if tool.name == "create_session")
        self.assertEqual(
            set(tool.input_schema.get("properties", {}).keys()),
            {"title", "description"},
        )
        self.assertEqual(tool.input_schema.get("required", []), [])

    def test_posts_only_session_metadata(self) -> None:
        response = {"data": {"id": "new-session"}}
        with mock.patch.object(srv.client, "_request", return_value=response) as req:
            self.assertEqual(
                srv.create_session(title="Research", description="Notes"),
                response,
            )
        req.assert_called_once_with(
            "POST",
            "/sessions",
            json={"title": "Research", "description": "Notes"},
        )

    def test_empty_metadata_posts_empty_json_object(self) -> None:
        response = {"data": {"id": "new-session"}}
        with mock.patch.object(srv.client, "_request", return_value=response) as req:
            self.assertEqual(srv.create_session(), response)
        req.assert_called_once_with("POST", "/sessions", json={})


if __name__ == "__main__":
    unittest.main()
