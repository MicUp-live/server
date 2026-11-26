#!/usr/bin/env python3
"""check-hotspots.py — Enforced maintainability hotspot baseline and change policy (M6).

Evaluates production source files against scripts/hotspot-baseline.json:
1. Enforced: Every entry in baseline_files and exceptions must exist on disk,
   have a valid positive max_lines ceiling (with measured_lines <= max_lines),
   and stay at or below its max_lines ceiling.
2. Enforced: Every entry in exceptions must declare non-empty owner, rationale,
   and removal_condition fields. Every entry in baseline_files must declare
   non-empty owner and rationale fields.
3. Enforced: Any untracked production file under tracked_roots (internal/, cmd/,
   web/src/, excluding *_test.go and *.test.{js,jsx}) exceeding default_max_lines
   (600 lines) fails the check unless explicitly added to the reviewed baseline
   or exceptions in scripts/hotspot-baseline.json.
4. Advisory: Untracked production files in the warning band
   (advisory_warn_lines < lines <= default_max_lines) and tracked files with
   headroom below their ceiling are reported as advisory signals without failing.

Includes --self-test to run isolated temporary-directory checks proving:
- Accepted baseline passes (exit 0)
- Tracked baseline ceiling violation fails (exit nonzero)
- Untracked file exceeding default_max_lines fails (exit nonzero)
- Documented exception with owner + removal_condition passes within ceiling
- Exception exceeding its ceiling or missing owner / removal_condition fails
- Missing tracked file fails
"""

from __future__ import annotations

import argparse
import copy
import json
import pathlib
import sys
import tempfile


REPO_ROOT = pathlib.Path(__file__).resolve().parent.parent
DEFAULT_BASELINE_PATH = REPO_ROOT / "scripts" / "hotspot-baseline.json"


def count_lines(path: pathlib.Path) -> int:
    """Return newline count matching `wc -l` semantics (or total lines if no trailing newline)."""
    data = path.read_bytes()
    if not data:
        return 0
    newlines = data.count(b"\n")
    return newlines if data.endswith(b"\n") else newlines + 1


def is_tracked_source(rel_posix: str, policy: dict) -> bool:
    excluded_suffixes = tuple(policy.get("excluded_suffixes", ["_test.go", ".test.js", ".test.jsx"]))
    if rel_posix.endswith(excluded_suffixes):
        return False
    tracked_exts = tuple(policy.get("tracked_extensions", [".go", ".js", ".jsx"]))
    return rel_posix.endswith(tracked_exts)


def collect_source_files(root: pathlib.Path, policy: dict) -> dict[str, int]:
    files: dict[str, int] = {}
    for tracked_root in policy.get("tracked_roots", ["internal", "cmd", "web/src"]):
        base = root / tracked_root
        if not base.exists():
            continue
        for p in sorted(base.rglob("*")):
            if not p.is_file():
                continue
            rel = p.relative_to(root).as_posix()
            if is_tracked_source(rel, policy):
                files[rel] = count_lines(p)
    return files


def validate_entry_metadata(
    rel_path: str,
    entry: dict,
    *,
    is_exception: bool,
    default_max_lines: int,
) -> list[str]:
    errors: list[str] = []
    kind = "exception" if is_exception else "baseline"
    if not isinstance(entry, dict):
        return [f"{kind} entry '{rel_path}' must be a JSON object"]

    max_lines = entry.get("max_lines")
    if not isinstance(max_lines, int) or isinstance(max_lines, bool) or max_lines <= 0:
        errors.append(f"{kind} entry '{rel_path}' must define a positive integer max_lines")

    measured_lines = entry.get("measured_lines")
    if not isinstance(measured_lines, int) or isinstance(measured_lines, bool) or measured_lines <= 0:
        errors.append(f"{kind} entry '{rel_path}' must define a positive integer measured_lines")
    elif isinstance(max_lines, int) and measured_lines > max_lines:
        errors.append(
            f"{kind} entry '{rel_path}' has measured_lines ({measured_lines}) > max_lines ({max_lines})"
        )

    owner = entry.get("owner")
    if not isinstance(owner, str) or not owner.strip():
        errors.append(f"{kind} entry '{rel_path}' is missing a non-empty 'owner'")

    rationale = entry.get("rationale")
    if not isinstance(rationale, str) or not rationale.strip():
        errors.append(f"{kind} entry '{rel_path}' is missing a non-empty 'rationale'")

    if is_exception:
        removal = entry.get("removal_condition")
        if not isinstance(removal, str) or not removal.strip():
            errors.append(
                f"exception entry '{rel_path}' is missing a non-empty 'removal_condition'"
            )
    else:
        if isinstance(max_lines, int) and max_lines > 1000:
            errors.append(
                f"baseline entry '{rel_path}' has max_lines={max_lines} (> 1000); "
                f"files over 1000 lines must be recorded under 'exceptions' with a removal_condition"
            )

    return errors


def evaluate_hotspots(
    root: pathlib.Path,
    baseline_data: dict,
    *,
    quiet: bool = False,
) -> tuple[int, list[str], list[str]]:
    """Evaluate repository files at `root` against `baseline_data`.

    Returns (exit_code, violations, advisories).
    """
    violations: list[str] = []
    advisories: list[str] = []

    if not isinstance(baseline_data, dict):
        return 1, ["Baseline configuration must be a JSON object"], []

    policy = baseline_data.get("policy")
    if not isinstance(policy, dict):
        return 1, ["Baseline configuration missing 'policy' object"], []

    default_max = policy.get("default_max_lines")
    advisory_warn = policy.get("advisory_warn_lines")
    if not isinstance(default_max, int) or default_max <= 0:
        return 1, ["policy.default_max_lines must be a positive integer"], []
    if not isinstance(advisory_warn, int) or advisory_warn <= 0 or advisory_warn >= default_max:
        return 1, ["policy.advisory_warn_lines must be a positive integer < default_max_lines"], []

    baseline_files = baseline_data.get("baseline_files")
    exceptions = baseline_data.get("exceptions")
    if not isinstance(baseline_files, dict):
        return 1, ["Baseline configuration missing 'baseline_files' object"], []
    if not isinstance(exceptions, dict):
        return 1, ["Baseline configuration missing 'exceptions' object"], []

    overlap = set(baseline_files.keys()) & set(exceptions.keys())
    if overlap:
        for dup in sorted(overlap):
            violations.append(
                f"File '{dup}' cannot appear in both 'baseline_files' and 'exceptions'"
            )

    source_files = collect_source_files(root, policy)

    rows: list[tuple[str, str, int, int, str]] = []

    # 1. Validate tracked baseline_files
    for rel_path, entry in baseline_files.items():
        meta_errs = validate_entry_metadata(
            rel_path, entry, is_exception=False, default_max_lines=default_max
        )
        violations.extend(meta_errs)
        full_path = root / rel_path
        if not full_path.is_file():
            violations.append(
                f"Tracked baseline file '{rel_path}' does not exist on disk "
                f"(update scripts/hotspot-baseline.json if removed or renamed)"
            )
            continue
        actual_lines = source_files.get(rel_path, count_lines(full_path))
        max_lines = entry.get("max_lines", default_max) if isinstance(entry, dict) else default_max
        category = entry.get("category", "baseline") if isinstance(entry, dict) else "baseline"
        if isinstance(max_lines, int) and actual_lines > max_lines:
            status = "FAIL (Exceeded)"
            violations.append(
                f"Baseline ceiling exceeded for '{rel_path}': {actual_lines} lines > "
                f"max_lines {max_lines} (owner: {entry.get('owner', 'unknown')})"
            )
        else:
            status = "PASS (Baseline)"
        rows.append((rel_path, category, actual_lines, max_lines, status))

    # 2. Validate documented exceptions
    for rel_path, entry in exceptions.items():
        meta_errs = validate_entry_metadata(
            rel_path, entry, is_exception=True, default_max_lines=default_max
        )
        violations.extend(meta_errs)
        full_path = root / rel_path
        if not full_path.is_file():
            violations.append(
                f"Documented exception file '{rel_path}' does not exist on disk "
                f"(update scripts/hotspot-baseline.json if removed or renamed)"
            )
            continue
        actual_lines = source_files.get(rel_path, count_lines(full_path))
        max_lines = entry.get("max_lines", default_max) if isinstance(entry, dict) else default_max
        category = entry.get("category", "exception") if isinstance(entry, dict) else "exception"
        if isinstance(max_lines, int) and actual_lines > max_lines:
            status = "FAIL (Exception)"
            violations.append(
                f"Exception ceiling exceeded for '{rel_path}': {actual_lines} lines > "
                f"max_lines {max_lines} (owner: {entry.get('owner', 'unknown')})"
            )
        else:
            status = "PASS (Exception)"
        rows.append((rel_path, f"{category}*", actual_lines, max_lines, status))

    # 3. Scan all untracked production source files against default_max_lines and advisory_warn_lines
    tracked_paths = set(baseline_files.keys()) | set(exceptions.keys())
    for rel_path, actual_lines in sorted(
        source_files.items(), key=lambda item: (-item[1], item[0])
    ):
        if rel_path in tracked_paths:
            continue
        if actual_lines > default_max:
            violations.append(
                f"Untracked file '{rel_path}' has {actual_lines} lines, exceeding "
                f"default_max_lines ({default_max}). Extract responsibilities or "
                f"register a reviewed baseline/exception in scripts/hotspot-baseline.json."
            )
            rows.append((rel_path, "untracked", actual_lines, default_max, "FAIL (Untracked)"))
        elif actual_lines > advisory_warn:
            advisories.append(
                f"Advisory: untracked file '{rel_path}' is {actual_lines} lines "
                f"(approaching {default_max}-line default limit; warn threshold {advisory_warn})"
            )

    if not quiet:
        measured_at = baseline_data.get("measured_at", "unknown")
        print(
            f"Maintainability Policy (baseline measured {measured_at}; "
            f"default_max_lines={default_max}, advisory_warn_lines={advisory_warn}):"
        )
        print(
            f"{'Tracked File':<48} {'Category':<20} {'Lines':>7} {'Ceiling':>8} {'Result':<16}"
        )
        print(
            f"{'-' * 48} {'-' * 20} {'-' * 7} {'-' * 8} {'-' * 16}"
        )
        for rel_path, category, actual_lines, max_lines, status in rows:
            print(
                f"{rel_path:<48} {category:<20} {actual_lines:>7} {max_lines:>8} {status:<16}"
            )

        if advisories:
            print("\nAdvisory Warnings (non-blocking signals):")
            for adv in advisories:
                print(f"  - {adv}")

        if violations:
            print("\nEnforced Maintainability Policy Violations:", file=sys.stderr)
            for v in violations:
                print(f"  [FAIL] {v}", file=sys.stderr)
            print(
                "\nMaintainability check FAILED. Reduce responsibility in the violating file "
                "or record an explicit reviewed baseline/exception change in scripts/hotspot-baseline.json.",
                file=sys.stderr,
            )
        else:
            print(
                f"\nEnforced maintainability check passed "
                f"({len(baseline_files)} baseline files, {len(exceptions)} documented exceptions, "
                f"{len(source_files)} total production files scanned)."
            )

    return (1 if violations else 0), violations, advisories


def write_lines(path: pathlib.Path, num_lines: int) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("// line\n" * num_lines, encoding="utf-8")


def run_self_tests(baseline_path: pathlib.Path) -> int:
    """Verify policy enforcement against the real repository and isolated temporary fixtures."""
    raw = json.loads(baseline_path.read_text(encoding="utf-8"))

    # Case 1: Real repository with accepted baseline must pass
    code, violations, _ = evaluate_hotspots(REPO_ROOT, raw, quiet=True)
    if code != 0 or violations:
        print(f"SELF-TEST FAIL [1]: real repo baseline failed: {violations}", file=sys.stderr)
        return 1

    with tempfile.TemporaryDirectory(prefix="micup-hotspot-selftest-") as tmp:
        tmp_root = pathlib.Path(tmp)
        fixture_policy = {
            "version": 1,
            "measured_at": "2026-10-18",
            "policy": {
                "default_max_lines": 600,
                "advisory_warn_lines": 500,
                "tracked_roots": ["internal", "cmd", "web/src"],
                "tracked_extensions": [".go", ".js", ".jsx"],
                "excluded_suffixes": ["_test.go", ".test.js", ".test.jsx"],
            },
            "baseline_files": {
                "internal/handlers/example.go": {
                    "category": "backend_adapter",
                    "measured_lines": 650,
                    "max_lines": 650,
                    "owner": "internal/handlers maintainers",
                    "rationale": "Tracked handler baseline fixture.",
                },
                "web/src/pages/Coordinator.jsx": {
                    "category": "frontend_decomposed",
                    "measured_lines": 300,
                    "max_lines": 400,
                    "owner": "web/src/pages maintainers",
                    "rationale": "Decomposed coordinator fixture.",
                },
            },
            "exceptions": {
                "internal/store/dynamo/big_store.go": {
                    "category": "backend_store",
                    "measured_lines": 1200,
                    "max_lines": 1200,
                    "owner": "internal/store/dynamo maintainers",
                    "rationale": "Consolidates atomic transactions.",
                    "removal_condition": "Remove when legacy compatibility helpers are retired.",
                }
            },
        }

        write_lines(tmp_root / "internal/handlers/example.go", 650)
        write_lines(tmp_root / "web/src/pages/Coordinator.jsx", 300)
        write_lines(tmp_root / "internal/store/dynamo/big_store.go", 1200)
        write_lines(tmp_root / "internal/small/ok.go", 200)
        write_lines(tmp_root / "internal/small/warn_band.go", 550)
        # Test files should be ignored even when large
        write_lines(tmp_root / "internal/store/dynamo/big_store_test.go", 2500)
        write_lines(tmp_root / "web/src/pages/Coordinator.test.jsx", 1500)

        # Case 2: Clean isolated fixture with advisory warning and valid exception passes
        code, violations, advisories = evaluate_hotspots(tmp_root, fixture_policy, quiet=True)
        if code != 0 or violations:
            print(f"SELF-TEST FAIL [2]: clean fixture failed: {violations}", file=sys.stderr)
            return 1
        if len(advisories) != 1 or "internal/small/warn_band.go" not in advisories[0]:
            print(
                f"SELF-TEST FAIL [2b]: expected 1 advisory for warn_band.go, got: {advisories}",
                file=sys.stderr,
            )
            return 1

        # Case 3: Deliberate baseline ceiling violation exits nonzero
        write_lines(tmp_root / "internal/handlers/example.go", 651)
        code, violations, _ = evaluate_hotspots(tmp_root, fixture_policy, quiet=True)
        if code == 0 or not any("Baseline ceiling exceeded" in v and "example.go" in v for v in violations):
            print(
                f"SELF-TEST FAIL [3]: baseline ceiling violation did not fail as expected: {violations}",
                file=sys.stderr,
            )
            return 1
        write_lines(tmp_root / "internal/handlers/example.go", 650)

        # Case 4: Deliberate untracked file exceeding default_max_lines exits nonzero
        untracked_violation = tmp_root / "web/src/components/NewMonolith.jsx"
        write_lines(untracked_violation, 601)
        code, violations, _ = evaluate_hotspots(tmp_root, fixture_policy, quiet=True)
        if code == 0 or not any("Untracked file 'web/src/components/NewMonolith.jsx'" in v for v in violations):
            print(
                f"SELF-TEST FAIL [4]: untracked file > default_max_lines did not fail: {violations}",
                file=sys.stderr,
            )
            return 1
        untracked_violation.unlink()

        # Case 5: Documented exception exceeding its ceiling exits nonzero
        write_lines(tmp_root / "internal/store/dynamo/big_store.go", 1201)
        code, violations, _ = evaluate_hotspots(tmp_root, fixture_policy, quiet=True)
        if code == 0 or not any("Exception ceiling exceeded" in v and "big_store.go" in v for v in violations):
            print(
                f"SELF-TEST FAIL [5]: exception ceiling violation did not fail: {violations}",
                file=sys.stderr,
            )
            return 1
        write_lines(tmp_root / "internal/store/dynamo/big_store.go", 1200)

        # Case 6: Exception missing removal_condition or owner exits nonzero
        bad_exc_policy = copy.deepcopy(fixture_policy)
        bad_exc_policy["exceptions"]["internal/store/dynamo/big_store.go"]["removal_condition"] = "   "
        code, violations, _ = evaluate_hotspots(tmp_root, bad_exc_policy, quiet=True)
        if code == 0 or not any("removal_condition" in v for v in violations):
            print(
                f"SELF-TEST FAIL [6a]: exception missing removal_condition did not fail: {violations}",
                file=sys.stderr,
            )
            return 1

        bad_owner_policy = copy.deepcopy(fixture_policy)
        bad_owner_policy["exceptions"]["internal/store/dynamo/big_store.go"]["owner"] = ""
        code, violations, _ = evaluate_hotspots(tmp_root, bad_owner_policy, quiet=True)
        if code == 0 or not any("missing a non-empty 'owner'" in v for v in violations):
            print(
                f"SELF-TEST FAIL [6b]: exception missing owner did not fail: {violations}",
                file=sys.stderr,
            )
            return 1

        # Case 7: Missing tracked file on disk exits nonzero
        (tmp_root / "web/src/pages/Coordinator.jsx").unlink()
        code, violations, _ = evaluate_hotspots(tmp_root, fixture_policy, quiet=True)
        if code == 0 or not any("does not exist on disk" in v and "Coordinator.jsx" in v for v in violations):
            print(
                f"SELF-TEST FAIL [7]: missing tracked file did not fail: {violations}",
                file=sys.stderr,
            )
            return 1

    print(
        "Hotspot policy self-tests passed (7/7 scenarios: accepted repo baseline, "
        "advisory warning band, baseline ceiling violation, untracked file violation, "
        "exception ceiling violation, exception owner/removal_condition metadata validation, "
        "and missing tracked file detection)."
    )
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Enforce MicUp.live maintainability hotspot baseline and exception policy."
    )
    parser.add_argument(
        "--baseline",
        type=pathlib.Path,
        default=DEFAULT_BASELINE_PATH,
        help="Path to hotspot-baseline.json (default: scripts/hotspot-baseline.json)",
    )
    parser.add_argument(
        "--root",
        type=pathlib.Path,
        default=REPO_ROOT,
        help="Repository root directory to scan",
    )
    parser.add_argument(
        "--self-test",
        action="store_true",
        help="Run isolated temporary-directory self-tests verifying pass/fail/exception behavior",
    )
    args = parser.parse_args()

    if not args.baseline.is_file():
        print(f"Error: hotspot baseline file not found: {args.baseline}", file=sys.stderr)
        return 1

    if args.self_test:
        return run_self_tests(args.baseline)

    baseline_data = json.loads(args.baseline.read_text(encoding="utf-8"))
    code, _, _ = evaluate_hotspots(args.root.resolve(), baseline_data, quiet=False)
    return code


if __name__ == "__main__":
    sys.exit(main())
