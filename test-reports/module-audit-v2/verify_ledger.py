#!/usr/bin/env python3
"""Deterministic validator for test-reports/module-audit-v2/{coverage,wave0-foundation}.json.

Checks against live `git ls-files`:
  1. Zero uncovered files across units + support_files.
  2. Zero duplicate assignments across or within units and support_files.
  3. Zero stale/missing paths absent from `git ls-files`.
  4. Zero executable, build/runtime config, stylesheet, or live-doc files leaked into
     `support_files` (explicitly allowlisting vendored `frontend/public/tdesign-icons/`
     and audit artifacts under `test-reports/`).
  5. Exact `summary` count parity, unit schema conformance, and `wave0-foundation.json`
     integrity.
  6. `git check-attr export-ignore test-reports/module-audit-v2/coverage.json` == `set`.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from collections import Counter
from pathlib import Path

VALID_WAVES = ("W1", "W2", "W3", "W4", "W5", "W6", "W7", "W8")
VALID_UNIT_STATUSES = {
    "pending",
    "pending_reaudit",
    "verified_no_findings",
    "verified_findings_fixed",
}
VALID_SUPPORT_CATEGORIES = (
    "license_or_notice",
    "historical_changelog_or_spec",
    "audit_ledger_or_archived_report",
    "static_binary_or_vendor_asset",
)

HISTORICAL_DOC_ALLOWLIST = {
    "CHANGELOG.md",
    "cli/CHANGELOG.md",
    "mcp-server/CHANGELOG.md",
    "BRANCH_GUIDE.md",
    "WEKNORA_RESIDUE_REMEDIATION.md",
    "AUDIT_REPORT.md",
}

LICENSE_EXACT_ALLOWLIST = {
    "LICENSE",
    "THIRD_PARTY_NOTICES.md",
    "REBRAND_RESERVED.md",
    "dataset/README",
    "dataset/README_zh.md",
}

LICENSE_BASENAME_ALLOWLIST = {
    "LICENSE",
    "LICENSE.GENYEE",
    "THIRD_PARTY_NOTICES.md",
    "BROWSERSKILL-LICENSE",
    "LOBE-ICONS-LICENSE",
    "NOTICES",
}

BINARY_ASSET_EXTENSIONS = {
    ".png",
    ".svg",
    ".webp",
    ".ico",
    ".jpg",
    ".jpeg",
    ".gif",
    ".wav",
    ".woff",
    ".woff2",
    ".ttf",
    ".eot",
    ".parquet",
    ".pdf",
    ".doc",
    ".docx",
    ".xls",
    ".xlsb",
    ".xlsx",
    ".ppt",
    ".pptx",
    ".epub",
    ".odp",
    ".ods",
    ".odt",
    ".rtf",
}

BINARY_ASSET_EXACT_ALLOWLIST = {
    "internal/infrastructure/legacyxls/testdata/fuzz/FuzzRead/71c8bf40ea78fcf4",
    "packages/.gitkeep",
}

FORBIDDEN_SUPPORT_EXTENSIONS = {
    ".go",
    ".ts",
    ".tsx",
    ".mts",
    ".vue",
    ".mjs",
    ".rs",
    ".c",
    ".h",
    ".rb",
    ".sql",
    ".proto",
    ".sbpl",
    ".sh",
    ".yaml",
    ".yml",
    ".toml",
    ".mod",
    ".sum",
    ".conf",
    ".service",
    ".less",
    ".css",
    ".html",
}


def git_ls_files(repo_root: Path) -> list[str]:
    raw = subprocess.check_output(
        ["git", "ls-files", "-z"],
        cwd=repo_root,
    ).decode("utf-8")
    if not raw:
        return []
    return sorted(raw.rstrip("\0").split("\0"))


def check_export_ignore(repo_root: Path, rel_path: str) -> str:
    out = subprocess.check_output(
        ["git", "check-attr", "export-ignore", "--", rel_path],
        cwd=repo_root,
    ).decode("utf-8").strip()
    # Format: "<path>: export-ignore: <value>"
    parts = out.rsplit(": ", 1)
    return parts[-1] if len(parts) == 2 else "unspecified"


def validate_support_file(category: str, path: str) -> str | None:
    base = os.path.basename(path)
    ext = os.path.splitext(path)[1].lower()

    if base == "Makefile" or base.startswith("Dockerfile") or base.startswith("docker-compose"):
        return f"build/container definition leaked into support_files[{category}]: {path}"

    if category == "license_or_notice":
        if (
            path in LICENSE_EXACT_ALLOWLIST
            or path.startswith(("licenses/", "OFFLINE_LICENSES/"))
            or base in LICENSE_BASENAME_ALLOWLIST
        ):
            return None
        return f"non-license file in license_or_notice: {path}"

    if category == "historical_changelog_or_spec":
        if path in HISTORICAL_DOC_ALLOWLIST:
            return None
        return f"unexpected file in historical_changelog_or_spec: {path}"

    if category == "audit_ledger_or_archived_report":
        if path.startswith("test-reports/"):
            return None
        return f"non-test-reports file in audit_ledger_or_archived_report: {path}"

    if category == "static_binary_or_vendor_asset":
        if path.startswith("frontend/public/tdesign-icons/"):
            return None
        if ext in FORBIDDEN_SUPPORT_EXTENSIONS or ext in {".py", ".js", ".md", ".json"}:
            return f"executable/config/live-doc file leaked into static_binary_or_vendor_asset: {path}"
        if ext in BINARY_ASSET_EXTENSIONS or path in BINARY_ASSET_EXACT_ALLOWLIST:
            return None
        return f"unrecognized asset type in static_binary_or_vendor_asset: {path}"

    return f"unknown support_files category {category!r} for {path}"


def validate(repo_root: Path, ledger_path: Path, wave0_path: Path) -> tuple[list[str], dict]:
    errors: list[str] = []

    if not ledger_path.is_file():
        return [f"missing coverage ledger: {ledger_path}"], {}
    if not wave0_path.is_file():
        return [f"missing Wave 0 evidence file: {wave0_path}"], {}

    with ledger_path.open("r", encoding="utf-8") as f:
        ledger = json.load(f)
    with wave0_path.open("r", encoding="utf-8") as f:
        wave0 = json.load(f)

    live_files = git_ls_files(repo_root)
    live_set = set(live_files)

    seen_owners: dict[str, list[str]] = {}
    units = ledger.get("units")
    if not isinstance(units, list) or not units:
        errors.append("coverage.json 'units' must be a non-empty list")
        units = []

    seen_unit_ids: set[str] = set()
    computed_units_by_wave: Counter[str] = Counter()
    computed_files_by_wave: Counter[str] = Counter()
    computed_units_by_status: Counter[str] = Counter()
    computed_units_by_legacy: Counter[str] = Counter()
    computed_unit_files_total = 0

    required_unit_keys = {
        "unit_id",
        "wave",
        "legacy_status",
        "status",
        "file_count",
        "tracked_files",
        "invariants_checked",
        "findings",
        "verification_commands",
        "evidence_file",
    }

    for idx, unit in enumerate(units):
        if not isinstance(unit, dict):
            errors.append(f"units[{idx}] is not an object")
            continue
        missing_keys = required_unit_keys - set(unit.keys())
        if missing_keys:
            errors.append(f"units[{idx}] ({unit.get('unit_id')}) missing keys: {sorted(missing_keys)}")

        uid = unit.get("unit_id")
        if not isinstance(uid, str) or not uid:
            errors.append(f"units[{idx}] has invalid unit_id: {uid!r}")
            continue
        if uid in seen_unit_ids:
            errors.append(f"duplicate unit_id: {uid}")
        seen_unit_ids.add(uid)

        wave = unit.get("wave")
        if wave not in VALID_WAVES:
            errors.append(f"unit {uid} has invalid wave: {wave!r}")

        status = unit.get("status")
        if status not in VALID_UNIT_STATUSES:
            errors.append(f"unit {uid} has invalid status: {status!r}")

        legacy_st = unit.get("legacy_status")
        if not isinstance(legacy_st, str) or not legacy_st:
            errors.append(f"unit {uid} has invalid legacy_status: {legacy_st!r}")

        if wave == "W8":
            if not str(legacy_st).startswith("audited"):
                errors.append(f"W8 unit {uid} must have audited* legacy_status, got {legacy_st!r}")
            if status == "pending":
                errors.append(f"W8 unit {uid} must use 'pending_reaudit' instead of 'pending'")
        elif status == "pending_reaudit":
            errors.append(f"non-W8 unit {uid} ({wave}) cannot have status 'pending_reaudit'")

        tfiles = unit.get("tracked_files")
        if not isinstance(tfiles, list) or not tfiles:
            errors.append(f"unit {uid} must have a non-empty tracked_files list")
            tfiles = []
        if tfiles != sorted(tfiles):
            errors.append(f"unit {uid} tracked_files are not deterministically sorted")
        if unit.get("file_count") != len(tfiles):
            errors.append(
                f"unit {uid} file_count ({unit.get('file_count')}) != len(tracked_files) ({len(tfiles)})"
            )

        for list_field in ("invariants_checked", "findings", "verification_commands"):
            if not isinstance(unit.get(list_field), list):
                errors.append(f"unit {uid} field {list_field!r} must be a list")
        if unit.get("evidence_file") is not None and not isinstance(unit.get("evidence_file"), str):
            errors.append(f"unit {uid} field 'evidence_file' must be null or string")

        computed_units_by_wave[wave] += 1
        computed_files_by_wave[wave] += len(tfiles)
        computed_units_by_status[status] += 1
        computed_units_by_legacy[legacy_st] += 1
        computed_unit_files_total += len(tfiles)

        for path in tfiles:
            seen_owners.setdefault(path, []).append(f"unit:{uid}")

    support = ledger.get("support_files")
    computed_support_by_cat: dict[str, int] = {}
    computed_support_total = 0
    if not isinstance(support, dict):
        errors.append("coverage.json 'support_files' must be an object keyed by category")
        support = {}
    else:
        extra_cats = set(support.keys()) - set(VALID_SUPPORT_CATEGORIES)
        missing_cats = set(VALID_SUPPORT_CATEGORIES) - set(support.keys())
        if extra_cats:
            errors.append(f"unexpected support_files categories: {sorted(extra_cats)}")
        if missing_cats:
            errors.append(f"missing support_files categories: {sorted(missing_cats)}")

        for cat in VALID_SUPPORT_CATEGORIES:
            cat_files = support.get(cat, [])
            if not isinstance(cat_files, list):
                errors.append(f"support_files[{cat!r}] must be a list")
                continue
            if cat_files != sorted(cat_files):
                errors.append(f"support_files[{cat!r}] is not deterministically sorted")
            computed_support_by_cat[cat] = len(cat_files)
            computed_support_total += len(cat_files)
            for path in cat_files:
                seen_owners.setdefault(path, []).append(f"support:{cat}")
                leak_err = validate_support_file(cat, path)
                if leak_err:
                    errors.append(leak_err)

    covered_set = set(seen_owners.keys())
    uncovered = sorted(live_set - covered_set)
    stale = sorted(covered_set - live_set)
    duplicates = {p: owners for p, owners in sorted(seen_owners.items()) if len(owners) > 1}

    if uncovered:
        errors.append(f"uncovered files ({len(uncovered)}): {uncovered[:10]}")
    if duplicates:
        sample = list(duplicates.items())[:10]
        errors.append(f"duplicate file assignments ({len(duplicates)}): {sample}")
    if stale:
        errors.append(f"stale/missing files not in git ls-files ({len(stale)}): {stale[:10]}")

    summary = ledger.get("summary")
    if not isinstance(summary, dict):
        errors.append("coverage.json 'summary' must be an object")
    else:
        expected_summary = {
            "total_tracked_files": len(live_files),
            "unit_count": len(units),
            "unit_files_total": computed_unit_files_total,
            "support_files_total": computed_support_total,
            "uncovered_files": len(uncovered),
            "duplicate_files": len(duplicates),
            "stale_files": len(stale),
            "units_by_wave": {w: computed_units_by_wave[w] for w in VALID_WAVES},
            "files_by_wave": {w: computed_files_by_wave[w] for w in VALID_WAVES},
            "units_by_status": dict(sorted(computed_units_by_status.items())),
            "units_by_legacy_status": dict(sorted(computed_units_by_legacy.items())),
            "support_files_by_category": computed_support_by_cat,
        }
        for key, exp_val in expected_summary.items():
            act_val = summary.get(key)
            if act_val != exp_val:
                errors.append(f"summary[{key!r}] mismatch: actual={act_val!r} != expected={exp_val!r}")

    export_attr_targets = (
        "test-reports/module-audit-v2/coverage.json",
        "test-reports/module-audit-v2/wave0-foundation.json",
        "test-reports/module-audit-v2/verify_ledger.py",
    )
    export_attrs: dict[str, str] = {}
    for rel in export_attr_targets:
        attr_val = check_export_ignore(repo_root, rel)
        export_attrs[rel] = attr_val
        if attr_val != "set":
            errors.append(f"git check-attr export-ignore {rel} returned {attr_val!r}, expected 'set'")

    # Validate wave0-foundation.json
    if wave0.get("wave") != "W0":
        errors.append(f"wave0-foundation.json wave={wave0.get('wave')!r}, expected 'W0'")
    if wave0.get("status") != "verified_findings_fixed":
        errors.append(
            f"wave0-foundation.json status={wave0.get('status')!r}, expected 'verified_findings_fixed'"
        )
    if not wave0.get("base_commit"):
        errors.append("wave0-foundation.json missing base_commit")
    commits = wave0.get("commits", [])
    if not isinstance(commits, list) or len(commits) != 2:
        errors.append(f"wave0-foundation.json commits must have 2 Wave 0 commits, got {len(commits)}")
    deleted_w0 = wave0.get("deleted_files", [])
    modified_w0 = wave0.get("modified_files", [])
    if len(deleted_w0) != 79:
        errors.append(f"wave0-foundation.json deleted_files count={len(deleted_w0)}, expected 79")
    if len(modified_w0) != 24:
        errors.append(f"wave0-foundation.json modified_files count={len(modified_w0)}, expected 24")
    still_present = sorted(set(deleted_w0) & live_set)
    if still_present:
        errors.append(f"wave0 deleted_files still present in git ls-files: {still_present[:5]}")
    missing_modified = sorted(set(modified_w0) - live_set)
    if missing_modified:
        errors.append(f"wave0 modified_files missing from git ls-files: {missing_modified[:5]}")
    if not wave0.get("preserved_invariants"):
        errors.append("wave0-foundation.json preserved_invariants must be non-empty")
    if not wave0.get("residue_audit_summary"):
        errors.append("wave0-foundation.json residue_audit_summary must be non-empty")

    report = {
        "total_git_ls_files": len(live_files),
        "unit_count": len(units),
        "unit_files_total": computed_unit_files_total,
        "support_files_total": computed_support_total,
        "uncovered_files": len(uncovered),
        "duplicate_files": len(duplicates),
        "stale_files": len(stale),
        "units_by_wave": {w: computed_units_by_wave[w] for w in VALID_WAVES},
        "files_by_wave": {w: computed_files_by_wave[w] for w in VALID_WAVES},
        "units_by_status": dict(sorted(computed_units_by_status.items())),
        "support_files_by_category": computed_support_by_cat,
        "export_ignore": export_attrs,
        "wave0_status": wave0.get("status"),
        "wave0_deleted_files": len(deleted_w0),
        "wave0_modified_files": len(modified_w0),
    }
    return errors, report


def main() -> int:
    parser = argparse.ArgumentParser(description="Validate v2 full-repo module audit ledger.")
    parser.add_argument(
        "--summary",
        action="store_true",
        help="Print deterministic coverage and wave breakdown summary.",
    )
    parser.add_argument(
        "--repo-root",
        type=Path,
        default=Path(__file__).resolve().parents[2],
        help="Repository root path.",
    )
    args = parser.parse_args()

    repo_root = args.repo_root.resolve()
    ledger_path = repo_root / "test-reports/module-audit-v2/coverage.json"
    wave0_path = repo_root / "test-reports/module-audit-v2/wave0-foundation.json"

    errors, report = validate(repo_root, ledger_path, wave0_path)

    if args.summary and report:
        print("=== GENYEE Module Audit v2 Ledger Summary ===")
        print(f"Total `git ls-files`:      {report['total_git_ls_files']}")
        print(
            f"Audited/Scheduled Units:   {report['unit_count']} units "
            f"({report['unit_files_total']} files)"
        )
        print(f"Non-Code Support Files:    {report['support_files_total']} files")
        print(
            f"Accounting Parity:         uncovered={report['uncovered_files']}, "
            f"duplicates={report['duplicate_files']}, stale={report['stale_files']}"
        )
        print("\nWave Partition (W1-W8):")
        for w in VALID_WAVES:
            print(
                f"  {w}: {report['units_by_wave'][w]:3d} units | "
                f"{report['files_by_wave'][w]:4d} files"
            )
        print("\nUnit Status Breakdown:")
        for st, cnt in report["units_by_status"].items():
            print(f"  {st:24s}: {cnt:3d} units")
        print("\nSupport Files Breakdown:")
        for cat, cnt in report["support_files_by_category"].items():
            print(f"  {cat:32s}: {cnt:3d} files")
        print("\nExport-Ignore Attributes:")
        for rel, val in report["export_ignore"].items():
            print(f"  {rel}: export-ignore={val}")
        print(
            f"\nWave 0 Foundation: status={report['wave0_status']}, "
            f"deleted={report['wave0_deleted_files']}, modified={report['wave0_modified_files']}"
        )

    if errors:
        print(f"\n[FAIL] {len(errors)} validation error(s) detected:", file=sys.stderr)
        for err in errors:
            print(f"  - {err}", file=sys.stderr)
        return 1

    print("\n[PASS] v2 module audit ledger verified (100% git ls-files coverage, 0 omissions).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
