# Brand asset sources

Logos identify supported integrations and the teams or products featured in the community showcase; their names and marks belong to their respective owners.

Paths below written as `public/brand/…`, `public/brands/…` and `public/product/…` live under `public/docs/_home/`, so the site serves them from `/docs/_home/` next to the rest of the documentation.

- GENYEE logo: `docs/images/logo.png` from the GENYEE repository, copied to `public/brand/genyee-logo.png`.
- GENYEE browser icon: copied from `frontend/public/favicon.ico` into the documentation (`/docs/favicon.ico`), which the homepage also uses.
- Feishu, GitLab, Tencent IMA, Notion, Yuque, RSS, WeCom, Slack, Telegram: copied from the frontend integration asset library.
- Seafile: copied from `frontend/src/assets/img/datasource-seafile.png` to `public/brands/seafile.png`.
- OpenAI, DeepSeek, Qwen, Hunyuan, Gemini, Ollama, MCP: static SVGs from [Lobe Icons](https://github.com/lobehub/lobe-icons), `@lobehub/icons-static-svg@1.95.0`. Upstream MIT license included with assets.
- Confluence, DingTalk: copied from the frontend integration asset library (`frontend/src/assets/img/datasource-confluence.svg`, `frontend/src/assets/img/im/dingtalk.svg`).
- Claude (Anthropic), Zhipu, Kimi (Moonshot), Volcengine, MiniMax: copied from `internal/models/providers/assets/`, which carries the same Lobe Icons static SVGs; covered by the included Lobe Icons MIT license.
- BrowserSkill: `frontend/src/assets/browserskill/logo.png` (MIT license in `public/brands/BROWSERSKILL-LICENSE`).
- Chrome: [official Chrome website asset](https://www.google.com/chrome/static/images/chrome-logo-m100.svg).
- File formats, REST API, CLI: generic interface icons, not brand logos.
- LiteLLM: [official documentation favicon](https://docs.litellm.ai/img/favicon.ico).

## Enterprise scenario showcase

The six scenario icons in `public/docs/_home/adopters/` are self-authored vector icons (MIT) representing typical enterprise deployment scenarios in `app/adopters.ts`:

| Scenario | Local file | License |
| --- | --- | --- |
| 智能制造与供应链 | `manufacturing.svg` | MIT (GENYEE) |
| 医疗健康服务 | `healthcare.svg` | MIT (GENYEE) |
| 出版传媒与智库 | `publishing.svg` | MIT (GENYEE) |
| 企业研发与 IT 运维 | `devops.svg` | MIT (GENYEE) |
| 政企内网私有化 | `private-cloud.svg` | MIT (GENYEE) |
| 企业客户服务与工单 | `customer-support.svg` | MIT (GENYEE) |

## Product screenshots

- Update reused screenshots in `../public/screenshots/`. The homepage prebuild/predev hook refreshes existing product copies, including the `sandbox-terminal.png` alias. Do not edit those copies separately.

- Release highlights also reuse `chat-source-location.png`, copied unchanged from `../public/screenshots/` to `public/docs/_home/product/`. Four features use compact sidebar navigation beside the screenshot, with all descriptions expanded. Attachment reading is a secondary link; its screenshot remains in the documentation. The standalone browser section presents task execution and extension connection as two expanded items on the left, switching the corresponding screenshot on the right.

- v0.8.3 gallery: `kb-image-gallery.png`, `kb-image-gallery-detail.png`, and `kb-image-vector-settings.png` are real product screenshots copied unchanged from `../public/screenshots/` to `public/docs/_home/product/`. The vector settings image shows the image-vector toggle. The gallery screenshots retain some image-load failure states from the captured interface. Screenshot contents and usage are documented in `../README.md`.

- Skill catalog and sandbox terminal: copied unchanged from `../public/screenshots/skill-catalog.png` and `sandbox-panel-terminal.png` to `public/product/skill-catalog.png` and `sandbox-terminal.png`, for the skills & sandbox gallery.
- Sandbox desktop (`public/product/sandbox-desktop.png`): the chat's sandbox panel on the 桌面 tab, showing the XFCE desktop of a desktop template. It shares the 终端与图形桌面 card with the terminal screenshot, switched inside the card.
- The permanent local-browser section reuses `local-browser-task.png` and `browser-connection.png` with the BrowserSkill icon and GitHub link from the v0.8.2 showcase. MCP Server is described within integrations; conversation controls stay in the Agent overview. `mcp-server-endpoint.png` and `chat-steer-queue.png` remain reference assets, without separate homepage showcases.
- Browser task (`public/product/local-browser-task.png`): a smart-reasoning chat driving the connected Chrome through BrowserSkill (GitHub release lookup, then filling the httpbin.org sample order form without submitting), with the in-chat task preview and pause / end controls.

- Wiki browser: copied unchanged from `../public/screenshots/wiki-browser.png` to `public/product/wiki-browser.png`. Shows the actual Wiki directory, linked page and source references using sample company policies.
- Wiki graph: `wiki-graph-groups.png` is a real product screenshot showing the full graph with grouped regions, theme labels and a node-type legend. It is copied unchanged from `../public/screenshots/` to `public/docs/_home/product/`.
- Historical Wiki graph and revision history: copied unchanged from `../public/screenshots/wiki-graph.png` and `wiki-revision-history.png` to matching files in `public/product/`, the revision-history image remains in the Wiki gallery, while the old graph image is retained for reference.
