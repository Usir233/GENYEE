#!/usr/bin/env python3
"""Compare the built-in vendor catalogs against models.dev and print a diff.

Development-time helper, never run at runtime: it reports numeric facts
(context window, max output, cost, new/retired ids) so a maintainer can update
internal/models/catalog/data/seed.json by hand. Behavioural facts (compat,
thinking format) are intentionally out of scope — models.dev does not carry
them and they must come from vendor documentation.

Usage:
    scripts/model_catalog_diff.py                # fetch https://models.dev/api.json
    scripts/model_catalog_diff.py --api api.json # use a downloaded copy
    scripts/model_catalog_diff.py --vendor deepseek
    scripts/model_catalog_diff.py --exit-code      # non-zero when anything differs

Findings are advisory: `+` is a model upstream has and we do not, `~` is a
numeric difference, `?` is an entry upstream does not list (often correct —
vendor-specific aliases and China-only ids are missing from models.dev).
"""

import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parent.parent
CATALOG = ROOT / "internal/models/catalog/data/models.generated.json"
SOURCES_FILE = ROOT / "scripts/model-catalog/sources.json"

# GENYEE vendor id -> models.dev provider id.
_SOURCES = json.loads(SOURCES_FILE.read_text(encoding="utf-8"))
PROVIDER_MAP: dict[str, str] = _SOURCES["models_dev"]["provider_map"]

MODELS_DEV_URL = "https://models.dev/api.json"
MAX_MODELS_DEV_BYTES = 64 * 1024 * 1024


def load_api(path: str | None) -> dict[str, Any]:
    """Load the models.dev catalog from a file, or fetch it once with a byte cap."""
    resolved = path or os.environ.get("MODELS_DEV_API")
    if resolved:
        return json.loads(Path(resolved).read_text(encoding="utf-8"))
    request = urllib.request.Request(
        MODELS_DEV_URL,
        headers={"User-Agent": "GENYEE-model-catalog-diff/1.0"},
    )
    try:
        with urllib.request.urlopen(request, timeout=60) as resp:
            raw = resp.read(MAX_MODELS_DEV_BYTES + 1)
            if len(raw) > MAX_MODELS_DEV_BYTES:
                raise SystemExit(
                    f"models.dev response exceeds {MAX_MODELS_DEV_BYTES} bytes.",
                )
            return json.loads(raw.decode("utf-8"))
    except urllib.error.URLError as err:
        raise SystemExit(
            f"failed to fetch {MODELS_DEV_URL}: {err}\n"
            "Download it manually and re-run with --api <file>, or set MODELS_DEV_API.",
        ) from err


def load_catalog(vendor: str) -> dict[str, dict[str, Any]]:
    entries = json.loads(CATALOG.read_text(encoding="utf-8"))["providers"].get(
        vendor,
        [],
    )
    return {m["id"]: m for m in entries if m.get("id")}


def compare(
    vendor: str,
    upstream: dict[str, Any],
    ours: dict[str, dict[str, Any]],
) -> list[str]:
    del vendor
    lines: list[str] = []
    up_models = upstream.get("models", {})
    for mid, spec in sorted(up_models.items()):
        mods = spec.get("modalities", {})
        if "text" not in mods.get("output", ["text"]):
            continue  # image / audio generation, out of scope
        entry = ours.get(mid)
        limit = spec.get("limit", {})
        cost = spec.get("cost", {})
        if entry is None:
            lines.append(
                f"  + {mid}  (new upstream: ctx={limit.get('context')} "
                f"out={limit.get('output')} "
                f"reasoning={spec.get('reasoning')} "
                f"released={spec.get('release_date')})",
            )
            continue
        diffs: list[str] = []
        ctx_limit = limit.get("context")
        if ctx_limit and entry.get("context_window") != ctx_limit:
            diffs.append(f"context_window {entry.get('context_window')} -> {ctx_limit}")
        out_limit = limit.get("output")
        if out_limit and entry.get("max_output_tokens") != out_limit:
            diffs.append(
                f"max_output_tokens {entry.get('max_output_tokens')} -> {out_limit}",
            )
        if bool(entry.get("reasoning")) != bool(spec.get("reasoning")):
            diffs.append(
                f"reasoning {entry.get('reasoning')} -> {spec.get('reasoning')}",
            )
        ours_cost = entry.get("cost") or {}
        for key, up_key in (
            ("input", "input"),
            ("output", "output"),
            ("cache_read", "cache_read"),
        ):
            if cost.get(up_key) is not None and ours_cost.get(key) != cost.get(up_key):
                diffs.append(f"cost.{key} {ours_cost.get(key)} -> {cost.get(up_key)}")
        if diffs:
            lines.append(f"  ~ {mid}: " + "; ".join(diffs))
    for mid in sorted(ours):
        if mid not in up_models:
            lines.append(
                f"  ? {mid}  (not in models.dev; keep if the vendor still documents it)",
            )
    return lines


def main() -> int:
    parser = argparse.ArgumentParser(
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("--api", help="path to a downloaded models.dev api.json")
    parser.add_argument("--vendor", help="limit to one GENYEE vendor id")
    parser.add_argument(
        "--exit-code",
        action="store_true",
        help="exit 1 when findings exist; default always exits 0",
    )
    args = parser.parse_args()

    api = load_api(args.api)
    vendors = [args.vendor] if args.vendor else sorted(PROVIDER_MAP)
    exit_code = 0
    for vendor in vendors:
        upstream_id = PROVIDER_MAP.get(vendor)
        if not upstream_id:
            print(f"== {vendor}: no models.dev mapping", file=sys.stderr)
            continue
        upstream = api.get(upstream_id)
        if upstream is None:
            print(
                f"== {vendor}: models.dev provider {upstream_id!r} missing",
                file=sys.stderr,
            )
            continue
        lines = compare(vendor, upstream, load_catalog(vendor))
        print(f"== {vendor} (models.dev: {upstream_id}) — {len(lines)} finding(s)")
        for line in lines:
            print(line)
        if lines:
            exit_code = 1
    return exit_code if args.exit_code else 0


if __name__ == "__main__":
    sys.exit(main())
