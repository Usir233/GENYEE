#!/usr/bin/env python3
"""Deterministic validator for test-reports/module-audit-v3/coverage.json.

Checks against live `git ls-files`:
  1. 100% file coverage across `units` + `support_files` (`uncovered=0`).
  2. Zero duplicate assignments across or within `units` and `support_files` (`duplicates=0`).
  3. Zero stale/missing paths absent from `git ls-files` (`stale=0`).
  4. Strict `support_files` allowlist enforcement (zero executable, config, stylesheet,
     or live-doc leaks into `support_files`).
  5. Exact `summary` parity, v3 unit schema conformance (`dimensions_verified`,
     `findings`, `commits`, `verification_commands`), `export-ignore` attributes, and
     zero host-path leaks (`host_path_leaks=0`).
  6. `--final` release gate: `pending == 0`, `pending_reaudit == 0`, and all recorded
     commits verified in `git rev-list HEAD`.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from collections import Counter
from pathlib import Path
from typing import Any

VALID_WAVES = ("W1", "W2", "W3", "W4", "W5", "W6", "W7", "W8")
VALID_UNIT_STATUSES = {
    "pending",
    "pending_reaudit",
    "verified_no_findings",
    "verified_findings_fixed",
}
CORE_DIMENSIONS = (
    "bug_reliability",
    "architecture_layering",
    "lint_formatting",
    "contract_sync",
)
WEB_DIMENSION = "modern_web_i18n"
VALID_DIMENSIONS = (*CORE_DIMENSIONS, WEB_DIMENSION)

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
    "ENGINEERING_STANDARDS.md",
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

HEX40_RE = re.compile(r"^[0-9a-f]{40}$")
HOST_PATH_RE = re.compile(
    "|".join(
        (
            "/" + "home/" + "cursor",
            "/" + "root/\\." + "gemini",
            "/" + "root/\\." + "cursor",
            "/" + "Users/" + "cy",
        )
    )
)


def git_ls_files(repo_root: Path) -> list[str]:
    raw = subprocess.check_output(
        ["git", "ls-files", "-z"],
        cwd=repo_root,
    ).decode("utf-8")
    if not raw:
        return []
    return sorted(raw.rstrip("\0").split("\0"))


def check_export_ignore(repo_root: Path, rel_path: str) -> str:
    out = (
        subprocess.check_output(
            ["git", "check-attr", "export-ignore", "--", rel_path],
            cwd=repo_root,
        )
        .decode("utf-8")
        .strip()
    )
    parts = out.rsplit(": ", 1)
    return parts[-1] if len(parts) == 2 else "unspecified"


def scan_host_path_leaks(repo_root: Path) -> list[str]:
    leaks: set[str] = set()
    grep_proc = subprocess.run(
        [
            "git",
            "grep",
            "-n",
            "-E",
            HOST_PATH_RE.pattern,
            "--",
            ".",
            ":!scripts/verify-commercial-compliance.sh",
        ],
        cwd=repo_root,
        capture_output=True,
        text=True,
        check=False,
    )
    if grep_proc.returncode == 0 and grep_proc.stdout.strip():
        for line in grep_proc.stdout.strip().splitlines():
            leaks.add(line)

    direct_targets: list[Path] = [repo_root / "ENGINEERING_STANDARDS.md"]
    v3_dir = repo_root / "test-reports/module-audit-v3"
    if v3_dir.is_dir():
        direct_targets.extend(sorted(p for p in v3_dir.rglob("*") if p.is_file()))

    for path in direct_targets:
        if not path.is_file():
            continue
        rel = path.relative_to(repo_root).as_posix()
        try:
            text = path.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            continue
        for lineno, line in enumerate(text.splitlines(), 1):
            if HOST_PATH_RE.search(line):
                leaks.add(f"{rel}:{lineno}:{line[:120]}")

    return sorted(leaks)


def requires_web_dimension(unit_id: str) -> bool:
    return unit_id.startswith(("frontend/", "packages/dsh-genyee", "website-docs"))


def validate_support_file(category: str, path: str) -> str | None:
    base = os.path.basename(path)
    ext = os.path.splitext(path)[1].lower()

    if base == "Makefile" or base.startswith(("Dockerfile", "docker-compose")):
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
            return (
                f"executable/config/live-doc file leaked into static_binary_or_vendor_asset: {path}"
            )
        if ext in BINARY_ASSET_EXTENSIONS or path in BINARY_ASSET_EXACT_ALLOWLIST:
            return None
        return f"unrecognized asset type in static_binary_or_vendor_asset: {path}"

    return f"unknown support_files category {category!r} for {path}"


def validate(
    repo_root: Path,
    ledger_path: Path,
    final: bool = False,
) -> tuple[list[str], dict[str, Any]]:
    errors: list[str] = []

    if not ledger_path.is_file():
        return [f"missing v3 coverage ledger: {ledger_path}"], {}

    with ledger_path.open("r", encoding="utf-8") as f:
        ledger = json.load(f)

    if ledger.get("schema_version") != "3.0":
        errors.append(
            f"coverage.json schema_version={ledger.get('schema_version')!r}, expected '3.0'"
        )
    standards_rel = ledger.get("standards_spec")
    standards_path = repo_root / "ENGINEERING_STANDARDS.md"
    if standards_rel != "ENGINEERING_STANDARDS.md" or not standards_path.is_file():
        errors.append("missing or mismatched ENGINEERING_STANDARDS.md specification")

    live_files = git_ls_files(repo_root)
    live_set = set(live_files)
    host_leaks = scan_host_path_leaks(repo_root)
    if host_leaks:
        errors.append(f"host path leaks detected ({len(host_leaks)}): {host_leaks[:5]}")

    seen_owners: dict[str, list[str]] = {}
    units = ledger.get("units")
    if not isinstance(units, list) or not units:
        errors.append("coverage.json 'units' must be a non-empty list")
        units = []

    seen_unit_ids: set[str] = set()
    computed_units_by_wave: Counter[str] = Counter()
    computed_files_by_wave: Counter[str] = Counter()
    computed_units_by_status: Counter[str] = Counter()
    computed_unit_files_total = 0
    total_findings = 0
    unique_commits: set[str] = set()

    required_unit_keys = {
        "unit_id",
        "wave",
        "status",
        "file_count",
        "tracked_files",
        "dimensions_verified",
        "findings",
        "commits",
        "verification_commands",
    }

    rev_list_head: set[str] = set()
    if final:
        rev_list_head = set(
            subprocess.check_output(["git", "rev-list", "HEAD"], cwd=repo_root)
            .decode("utf-8")
            .splitlines()
        )

    for idx, unit in enumerate(units):
        if not isinstance(unit, dict):
            errors.append(f"units[{idx}] is not an object")
            continue
        missing_keys = required_unit_keys - set(unit.keys())
        if missing_keys:
            errors.append(
                f"units[{idx}] ({unit.get('unit_id')}) missing keys: {sorted(missing_keys)}"
            )

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

        tfiles = unit.get("tracked_files")
        if not isinstance(tfiles, list) or not tfiles:
            errors.append(f"unit {uid} must have a non-empty tracked_files list")
            tfiles = []
        if tfiles != sorted(tfiles):
            errors.append(f"unit {uid} tracked_files are not deterministically sorted")
        if unit.get("file_count") != len(tfiles):
            errors.append(
                f"unit {uid} file_count ({unit.get('file_count')}) "
                f"!= len(tracked_files) ({len(tfiles)})"
            )

        dims = unit.get("dimensions_verified")
        if not isinstance(dims, dict):
            errors.append(f"unit {uid} 'dimensions_verified' must be an object")
            dims = {}
        else:
            unknown_dims = set(dims.keys()) - set(VALID_DIMENSIONS)
            if unknown_dims:
                errors.append(f"unit {uid} has unknown dimensions: {sorted(unknown_dims)}")
            missing_core = set(CORE_DIMENSIONS) - set(dims.keys())
            if missing_core:
                errors.append(f"unit {uid} missing core dimensions: {sorted(missing_core)}")
            if requires_web_dimension(uid) and WEB_DIMENSION not in dims:
                errors.append(f"web/docs unit {uid} must track {WEB_DIMENSION!r}")
            for dim_key, dim_val in dims.items():
                if not isinstance(dim_val, bool):
                    errors.append(
                        f"unit {uid} dimensions_verified[{dim_key!r}] must be bool, got {dim_val!r}"
                    )

        findings = unit.get("findings")
        if not isinstance(findings, list):
            errors.append(f"unit {uid} 'findings' must be a list")
            findings = []

        u_commits = unit.get("commits")
        if not isinstance(u_commits, list):
            errors.append(f"unit {uid} 'commits' must be a list")
            u_commits = []

        v_cmds = unit.get("verification_commands")
        if not isinstance(v_cmds, list):
            errors.append(f"unit {uid} 'verification_commands' must be a list")
            v_cmds = []

        for sha in u_commits:
            if not isinstance(sha, str) or not HEX40_RE.match(sha):
                errors.append(f"unit {uid} has invalid 40-char commit SHA in commits: {sha!r}")
            else:
                unique_commits.add(sha)
                if final and sha not in rev_list_head:
                    errors.append(f"--final unit {uid} commit {sha} not in git rev-list HEAD")

        for f_idx, finding in enumerate(findings):
            total_findings += 1
            if not isinstance(finding, dict):
                errors.append(f"unit {uid} findings[{f_idx}] must be an object")
                continue
            f_commit = finding.get("commit")
            if f_commit is not None:
                if not isinstance(f_commit, str) or not HEX40_RE.match(f_commit):
                    errors.append(
                        f"unit {uid} finding {finding.get('id', f_idx)} "
                        f"has invalid commit SHA: {f_commit!r}"
                    )
                else:
                    unique_commits.add(f_commit)
                    if final and f_commit not in rev_list_head:
                        errors.append(
                            f"--final unit {uid} finding {finding.get('id', f_idx)} "
                            f"commit {f_commit} not in git rev-list HEAD"
                        )

        if status in {"verified_no_findings", "verified_findings_fixed"}:
            unverified_dims = [k for k, v in dims.items() if v is not True]
            if unverified_dims:
                errors.append(
                    f"verified unit {uid} still has unverified dimensions: "
                    f"{sorted(unverified_dims)}"
                )
            if not v_cmds:
                errors.append(f"verified unit {uid} must record non-empty verification_commands")
            if status == "verified_findings_fixed":
                if not findings:
                    errors.append(f"unit {uid} has status verified_findings_fixed but 0 findings")
                has_commit = bool(u_commits) or any(
                    isinstance(item, dict) and bool(item.get("commit")) for item in findings
                )
                if not has_commit:
                    errors.append(
                        f"unit {uid} has status verified_findings_fixed but 0 recorded commit SHAs"
                    )
            elif findings:
                errors.append(
                    f"unit {uid} has status verified_no_findings but {len(findings)} finding(s)"
                )

        if isinstance(wave, str) and wave in VALID_WAVES:
            computed_units_by_wave[wave] += 1
            computed_files_by_wave[wave] += len(tfiles)
        if isinstance(status, str):
            computed_units_by_status[status] += 1
        computed_unit_files_total += len(tfiles)

        for path in tfiles:
            seen_owners.setdefault(path, []).append(f"unit:{uid}")

    support = ledger.get("support_files")
    computed_support_by_cat: dict[str, int] = {}
    computed_support_total = 0
    if not isinstance(support, dict):
        errors.append("coverage.json 'support_files' must be an object keyed by category")
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
        expected_summary: dict[str, Any] = {
            "total_tracked_files": len(live_files),
            "unit_count": len(units),
            "unit_files_total": computed_unit_files_total,
            "support_files_total": computed_support_total,
            "uncovered_files": len(uncovered),
            "duplicate_files": len(duplicates),
            "stale_files": len(stale),
            "host_path_leaks": len(host_leaks),
            "units_by_wave": {w: computed_units_by_wave[w] for w in VALID_WAVES},
            "files_by_wave": {w: computed_files_by_wave[w] for w in VALID_WAVES},
            "units_by_status": dict(sorted(computed_units_by_status.items())),
            "support_files_by_category": computed_support_by_cat,
        }
        for key, exp_val in expected_summary.items():
            act_val = summary.get(key)
            if act_val != exp_val:
                errors.append(
                    f"summary[{key!r}] mismatch: actual={act_val!r} != expected={exp_val!r}"
                )

    export_attr_targets = (
        "ENGINEERING_STANDARDS.md",
        "BRANCH_GUIDE.md",
        "REBRAND_RESERVED.md",
        "test-reports/module-audit-v3/coverage.json",
        "test-reports/module-audit-v3/verify_ledger.py",
    )
    export_attrs: dict[str, str] = {}
    for rel in export_attr_targets:
        attr_val = check_export_ignore(repo_root, rel)
        export_attrs[rel] = attr_val
        if attr_val != "set":
            errors.append(
                f"git check-attr export-ignore {rel} returned {attr_val!r}, expected 'set'"
            )

    if final:
        pending_cnt = computed_units_by_status.get("pending", 0)
        pending_reaudit_cnt = computed_units_by_status.get("pending_reaudit", 0)
        if pending_cnt != 0 or pending_reaudit_cnt != 0:
            errors.append(
                "--final requires 0 pending/pending_reaudit units, got "
                f"pending={pending_cnt}, pending_reaudit={pending_reaudit_cnt}"
            )

    report: dict[str, Any] = {
        "schema_version": ledger.get("schema_version"),
        "total_git_ls_files": len(live_files),
        "unit_count": len(units),
        "unit_files_total": computed_unit_files_total,
        "support_files_total": computed_support_total,
        "uncovered_files": len(uncovered),
        "duplicate_files": len(duplicates),
        "stale_files": len(stale),
        "host_path_leaks": len(host_leaks),
        "units_by_wave": {w: computed_units_by_wave[w] for w in VALID_WAVES},
        "files_by_wave": {w: computed_files_by_wave[w] for w in VALID_WAVES},
        "units_by_status": dict(sorted(computed_units_by_status.items())),
        "support_files_by_category": computed_support_by_cat,
        "export_ignore": export_attrs,
        "final_mode": final,
        "total_findings": total_findings,
        "unique_commits": len(unique_commits),
    }
    return errors, report


def main() -> int:
    parser = argparse.ArgumentParser(description="Validate v3 full-repo 5-dimension audit ledger.")
    parser.add_argument(
        "--summary",
        action="store_true",
        help="Print deterministic coverage and wave breakdown summary.",
    )
    parser.add_argument(
        "--final",
        action="store_true",
        help=(
            "Enforce final release gates: 0 pending/pending_reaudit units "
            "and all commits in git rev-list HEAD."
        ),
    )
    parser.add_argument(
        "--repo-root",
        type=Path,
        default=Path(__file__).resolve().parents[2],
        help="Repository root path.",
    )
    args = parser.parse_args()

    repo_root = args.repo_root.resolve()
    ledger_path = repo_root / "test-reports/module-audit-v3/coverage.json"

    errors, report = validate(repo_root, ledger_path, final=args.final)

    if args.summary and report:
        print("=== GENYEE Module Audit v3 Ledger Summary ===")
        print(f"Schema Version:            {report['schema_version']}")
        print(f"Total `git ls-files`:      {report['total_git_ls_files']}")
        print(
            f"Auditable Units:           {report['unit_count']} units "
            f"({report['unit_files_total']} files)"
        )
        print(f"Non-Code Support Files:    {report['support_files_total']} files")
        print(
            f"Accounting Parity:         uncovered={report['uncovered_files']}, "
            f"duplicates={report['duplicate_files']}, stale={report['stale_files']}, "
            f"host_path_leaks={report['host_path_leaks']}"
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
        if report["final_mode"]:
            print(
                f"\nFinal Release Gate:        findings={report['total_findings']}, "
                f"commits_verified={report['unique_commits']}"
            )

    if errors:
        print(f"\n[FAIL] {len(errors)} validation error(s) detected:", file=sys.stderr)
        for err in errors:
            print(f"  - {err}", file=sys.stderr)
        return 1

    print("\n[PASS] v3 module audit ledger verified (100% git ls-files coverage, 0 omissions).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
