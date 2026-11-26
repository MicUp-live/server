#!/usr/bin/env python3
"""Isolated local rehearsal for M9 smoke flow and M7/M8/M14 operator recovery CLIs.

Usage:
    python3 scripts/local/rehearse-m9-recovery.py
    python3 scripts/local/rehearse-m9-recovery.py --self-test

Prerequisites:
    - DynamoDB Local listening on loopback (default: http://127.0.0.1:8000)
    - LocalStack (S3 + SQS) listening on loopback (default: http://127.0.0.1:4566)
    - Go 1.26 and AWS CLI installed (via mise or PATH)

Safety guarantees:
    - Requires loopback HTTP endpoints (127.0.0.1, localhost, or ::1) and dummy
      local credentials (AWS_ACCESS_KEY_ID=local, AWS_SECRET_ACCESS_KEY=local).
    - Uses a unique per-run namespace ("micup-m9-drill-<run-id>-") to establish
      unambiguous resource ownership and refuses production or persistent
      "micup-local" names.
    - Refuses an occupied API port before any resource creation/mutation and
      verifies the launched API child process remains alive before every HTTP
      request.
    - Tracks drill tables even when schema bootstrap partially fails.
    - Aggregates cleanup failures, verifies all owned resources are gone, and
      exits nonzero listing remaining resource names if cleanup is incomplete.
"""

import argparse
import dataclasses
import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import Callable

REPO_ROOT = Path(__file__).resolve().parents[2]
BASE_DRILL_PREFIX = "micup-m9-drill-"

TABLE_SUFFIXES = [
    "users",
    "identity-claims",
    "sessions",
    "songbooks",
    "event-requests",
    "events",
    "passkeys",
    "webauthn-sessions",
    "catalog-versions",
    "import-jobs",
    "catalog-changes",
    "developer-keys",
    "rate-limits",
    "idempotency",
    "songbook-grants",
    "songbook-invitations",
]

ALLOWED_LOOPBACK_HOSTS = {"127.0.0.1", "localhost", "::1"}
ALLOWED_DUMMY_CREDS = {"local", "test", "dummy"}


@dataclasses.dataclass(frozen=True)
class DrillNamespace:
    run_id: str
    prefix: str
    bucket: str
    catalog_queue: str
    catalog_dlq: str
    import_queue: str
    import_dlq: str

    @property
    def queues(self) -> list[str]:
        return [
            self.catalog_dlq,
            self.catalog_queue,
            self.import_dlq,
            self.import_queue,
        ]


def make_drill_namespace(run_id: str | None = None) -> DrillNamespace:
    if not run_id:
        ts = datetime.now(timezone.utc).strftime("%m%d%H%M%S")
        run_id = f"{ts}-{os.getpid()}-{uuid.uuid4().hex[:6]}"
    clean_id = run_id.strip().lower()
    if not clean_id or any(c not in "abcdefghijklmnopqrstuvwxyz0123456789-" for c in clean_id):
        raise ValueError(f"Invalid drill run_id={run_id!r}; must be lowercase alphanumeric/hyphens")
    if "local" in clean_id or "prod" in clean_id:
        raise ValueError(f"Refusing unsafe drill run_id={run_id!r}")
    prefix = f"{BASE_DRILL_PREFIX}{clean_id}-"
    bucket = f"{prefix}bucket"
    if len(bucket) > 63:
        raise ValueError(f"Drill bucket name {bucket!r} exceeds 63-character S3 limit ({len(bucket)})")
    return DrillNamespace(
        run_id=clean_id,
        prefix=prefix,
        bucket=bucket,
        catalog_queue=f"{prefix}catalog-build",
        catalog_dlq=f"{prefix}catalog-build-dlq",
        import_queue=f"{prefix}import-jobs",
        import_dlq=f"{prefix}import-jobs-dlq",
    )


def require_loopback_url(name: str, url: str) -> str:
    parsed = urllib.parse.urlparse(url)
    if parsed.scheme != "http" or parsed.hostname not in ALLOWED_LOOPBACK_HOSTS:
        raise ValueError(
            f"Refusing non-loopback {name}={url!r}; must use http://127.0.0.1, http://localhost, or http://[::1]"
        )
    if "amazonaws.com" in url.lower() or "micup.live" in url.lower():
        raise ValueError(f"Refusing cloud/production endpoint in {name}={url!r}")
    return url.rstrip("/")


def validate_drill_isolation(
    dynamo_endpoint: str,
    localstack_endpoint: str,
    env: dict[str, str],
    ns: DrillNamespace,
) -> None:
    require_loopback_url("DYNAMO_ENDPOINT", dynamo_endpoint)
    require_loopback_url("LOCALSTACK_ENDPOINT", localstack_endpoint)

    for key in ("AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"):
        val = env.get(key, "")
        if val not in ALLOWED_DUMMY_CREDS:
            raise ValueError(f"Refusing non-dummy credential for {key}")

    prefix = env.get("DYNAMO_TABLE_PREFIX", "")
    if prefix != ns.prefix or not prefix.startswith(BASE_DRILL_PREFIX) or "local" in prefix or "prod" in prefix:
        raise ValueError(f"Refusing unsafe DYNAMO_TABLE_PREFIX={prefix!r}")

    bucket = env.get("CATALOG_ASSET_BUCKET", "")
    if bucket != ns.bucket or not bucket.startswith(ns.prefix) or "local" in bucket or "prod" in bucket:
        raise ValueError(f"Refusing unsafe CATALOG_ASSET_BUCKET={bucket!r}")

    for q in ns.queues:
        if not q.startswith(ns.prefix) or "local" in q or "prod" in q:
            raise ValueError(f"Refusing unsafe SQS queue name {q!r}")


def ensure_api_port_available(port: int) -> None:
    if not (1 <= port <= 65535):
        raise ValueError(f"Invalid API port {port}")

    # 1. Refuse if any listener is already accepting connections on IPv4 or IPv6 loopback.
    for host in ("127.0.0.1", "::1"):
        try:
            with socket.create_connection((host, port), timeout=0.25):
                raise RuntimeError(
                    f"Refusing occupied API port {port}: existing process is accepting connections on {host}:{port}"
                )
        except RuntimeError:
            raise
        except OSError:
            pass

    # 2. Refuse if binding without SO_REUSEADDR fails on loopback or wildcard.
    for family, bind_host in ((socket.AF_INET, "127.0.0.1"), (socket.AF_INET, "0.0.0.0")):
        with socket.socket(family, socket.SOCK_STREAM) as sock:
            try:
                sock.bind((bind_host, port))
            except OSError as exc:
                raise RuntimeError(
                    f"Refusing occupied API port {port}: cannot bind {bind_host}:{port} ({exc})"
                ) from exc


def ensure_process_alive(proc: subprocess.Popen | None, name: str = "API server") -> None:
    if proc is None:
        raise RuntimeError(f"Launched {name} process is not running (None)")
    rc = proc.poll()
    if rc is not None:
        output = ""
        if proc.stdout is not None:
            try:
                output = proc.stdout.read()
            except Exception:
                output = ""
        detail = f": {output.strip()}" if output.strip() else ""
        raise RuntimeError(f"Launched {name} process (pid={proc.pid}) exited unexpectedly with code {rc}{detail}")


def terminate_process(proc: subprocess.Popen | None) -> None:
    if proc is None:
        return
    if proc.poll() is None:
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except Exception:
            proc.kill()
            try:
                proc.wait(timeout=2)
            except Exception:
                pass
    if proc.stdout is not None:
        try:
            proc.stdout.close()
        except Exception:
            pass


def resolve_binary(name: str) -> str:
    # Resolve via mise when available so direct execution does not invoke `mise exec`
    # (which reloads .mise.toml [env] and would overwrite DYNAMO_TABLE_PREFIX).
    mise_bin = shutil.which("mise")
    if mise_bin:
        res = subprocess.run(
            [mise_bin, "which", name],
            cwd=REPO_ROOT,
            text=True,
            capture_output=True,
            check=False,
        )
        if res.returncode == 0 and res.stdout.strip():
            return res.stdout.strip()
    found = shutil.which(name)
    if not found:
        raise RuntimeError(f"Required binary {name!r} not found in mise or PATH")
    return found


def format_display_cmd(args: list[str], go_bin: str, aws_bin: str) -> str:
    display = []
    for a in args:
        if a == go_bin:
            display.append("go")
        elif a == aws_bin:
            display.append("aws")
        else:
            display.append(a)
    return " ".join(display)


def run_cmd(
    args: list[str],
    env: dict[str, str],
    go_bin: str,
    aws_bin: str,
    check: bool = True,
    label: str | None = None,
) -> tuple[subprocess.CompletedProcess, float]:
    t0 = time.monotonic()
    res = subprocess.run(args, cwd=REPO_ROOT, env=env, text=True, capture_output=True, check=False)
    dt = time.monotonic() - t0
    cmd_str = format_display_cmd(args, go_bin, aws_bin)
    if label:
        print(f"\n=== [{label}] ({dt:.3f}s) ===")
        print(f"$ {cmd_str}")
        if res.stdout.strip():
            print(res.stdout.strip())
        if res.stderr.strip():
            print(res.stderr.strip())
    if check and res.returncode != 0:
        raise RuntimeError(
            f"Command failed ({res.returncode}): {cmd_str}\nSTDOUT:\n{res.stdout}\nSTDERR:\n{res.stderr}"
        )
    return res, dt


def http_req(
    api_base: str,
    method: str,
    path: str,
    body: dict | list | str | bytes | None = None,
    headers: dict[str, str] | None = None,
    api_proc: subprocess.Popen | None = None,
) -> tuple[int, dict[str, str], dict | list | str]:
    if api_proc is not None:
        ensure_process_alive(api_proc, "API server")
    url = f"{api_base}{path}" if path.startswith("/") else path
    req_headers: dict[str, str] = {}
    if headers:
        req_headers.update(headers)
    data = None
    if body is not None:
        if isinstance(body, (dict, list)):
            data = json.dumps(body).encode("utf-8")
            req_headers.setdefault("Content-Type", "application/json")
        elif isinstance(body, str):
            data = body.encode("utf-8")
        else:
            data = body
    req = urllib.request.Request(url, data=data, headers=req_headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            raw = resp.read().decode("utf-8")
            parsed = json.loads(raw) if raw.strip().startswith(("{", "[")) else raw
            status, resp_headers = resp.status, dict(resp.headers)
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8")
        try:
            parsed = json.loads(raw)
        except Exception:
            parsed = raw
        status, resp_headers = e.code, dict(e.headers)

    if api_proc is not None:
        ensure_process_alive(api_proc, "API server")
    return status, resp_headers, parsed


def wait_for_api_ready(api_proc: subprocess.Popen, api_base: str, max_attempts: int = 30) -> None:
    for _ in range(max_attempts):
        ensure_process_alive(api_proc, "API server")
        try:
            st, _, _ = http_req(api_base, "GET", "/api/v1/healthz", api_proc=api_proc)
            if st == 204:
                ensure_process_alive(api_proc, "API server")
                return
        except RuntimeError:
            raise
        except Exception:
            pass
        time.sleep(0.2)
    ensure_process_alive(api_proc, "API server")
    raise RuntimeError("API server did not become healthy within timeout")


def list_dynamo_tables(aws_bin: str, env: dict[str, str], dynamo_endpoint: str) -> list[str]:
    res = subprocess.run(
        [aws_bin, "dynamodb", "list-tables", "--endpoint-url", dynamo_endpoint, "--output", "json"],
        cwd=REPO_ROOT,
        env=env,
        text=True,
        capture_output=True,
        check=True,
    )
    return json.loads(res.stdout).get("TableNames", [])


def discover_owned_tables(aws_bin: str, env: dict[str, str], dynamo_endpoint: str, prefix: str) -> set[str]:
    return {t for t in list_dynamo_tables(aws_bin, env, dynamo_endpoint) if t.startswith(prefix)}


def get_local_prefix_counts(aws_bin: str, env: dict[str, str], dynamo_endpoint: str) -> dict[str, int]:
    counts: dict[str, int] = {}
    for s in TABLE_SUFFIXES:
        t = f"micup-local-{s}"
        res = subprocess.run(
            [
                aws_bin,
                "dynamodb",
                "scan",
                "--table-name",
                t,
                "--select",
                "COUNT",
                "--endpoint-url",
                dynamo_endpoint,
                "--output",
                "json",
            ],
            cwd=REPO_ROOT,
            env=env,
            text=True,
            capture_output=True,
            check=False,
        )
        if res.returncode == 0 and res.stdout.strip():
            counts[t] = json.loads(res.stdout).get("Count", 0)
    return counts


def list_s3_buckets(aws_bin: str, env: dict[str, str], localstack_endpoint: str) -> list[str]:
    res = subprocess.run(
        [aws_bin, "s3api", "list-buckets", "--endpoint-url", localstack_endpoint, "--output", "json"],
        cwd=REPO_ROOT,
        env=env,
        text=True,
        capture_output=True,
        check=True,
    )
    return [b["Name"] for b in json.loads(res.stdout).get("Buckets", [])]


def list_sqs_queues(aws_bin: str, env: dict[str, str], localstack_endpoint: str) -> list[str]:
    res = subprocess.run(
        [aws_bin, "sqs", "list-queues", "--endpoint-url", localstack_endpoint, "--output", "json"],
        cwd=REPO_ROOT,
        env=env,
        text=True,
        capture_output=True,
        check=True,
    )
    data = json.loads(res.stdout) if res.stdout.strip() else {}
    return data.get("QueueUrls", [])


def bootstrap_drill_resources(
    ns: DrillNamespace,
    env: dict[str, str],
    go_bin: str,
    aws_bin: str,
    dynamo_endpoint: str,
    localstack_endpoint: str,
    created_tables: set[str],
    created_buckets: set[str],
    created_queue_urls: set[str],
    bootstrap_fn: Callable[[], None] | None = None,
) -> None:
    run_cmd(
        [
            aws_bin,
            "s3api",
            "create-bucket",
            "--bucket",
            ns.bucket,
            "--create-bucket-configuration",
            "LocationConstraint=us-east-2",
            "--endpoint-url",
            localstack_endpoint,
        ],
        env,
        go_bin,
        aws_bin,
    )
    created_buckets.add(ns.bucket)

    for q in ns.queues:
        res, _ = run_cmd(
            [
                aws_bin,
                "sqs",
                "create-queue",
                "--queue-name",
                q,
                "--endpoint-url",
                localstack_endpoint,
                "--output",
                "json",
            ],
            env,
            go_bin,
            aws_bin,
        )
        q_url = json.loads(res.stdout).get("QueueUrl") or f"{localstack_endpoint}/000000000000/{q}"
        created_queue_urls.add(q_url)

    try:
        if bootstrap_fn is not None:
            bootstrap_fn()
        else:
            run_cmd(
                [go_bin, "run", "./cmd/local-bootstrap"],
                env,
                go_bin,
                aws_bin,
                label="Bootstrap drill DynamoDB schema",
            )
    finally:
        # Always discover and track any tables created under the per-run prefix,
        # even if schema bootstrap failed partway through.
        created_tables.update(discover_owned_tables(aws_bin, env, dynamo_endpoint, ns.prefix))


def cleanup_and_verify_owned_resources(
    ns: DrillNamespace,
    created_tables: set[str],
    created_buckets: set[str],
    created_queue_urls: set[str],
    aws_bin: str,
    go_bin: str,
    env: dict[str, str],
    dynamo_endpoint: str,
    localstack_endpoint: str,
    run_cmd_fn: Callable = run_cmd,
    list_tables_fn: Callable = list_dynamo_tables,
    list_buckets_fn: Callable = list_s3_buckets,
    list_queues_fn: Callable = list_sqs_queues,
) -> tuple[int, list[str], list[str]]:
    cleanup_errors: list[str] = []

    # Discover any resources in the unique per-run namespace that may have been created
    # before an interruption.
    try:
        for t in list_tables_fn(aws_bin, env, dynamo_endpoint):
            if t.startswith(ns.prefix):
                created_tables.add(t)
    except Exception as exc:
        cleanup_errors.append(f"pre-cleanup list-tables failed: {exc}")

    try:
        for b in list_buckets_fn(aws_bin, env, localstack_endpoint):
            if b.startswith(ns.prefix):
                created_buckets.add(b)
    except Exception as exc:
        cleanup_errors.append(f"pre-cleanup list-buckets failed: {exc}")

    try:
        for q_url in list_queues_fn(aws_bin, env, localstack_endpoint):
            if f"/{ns.prefix}" in q_url:
                created_queue_urls.add(q_url)
    except Exception as exc:
        cleanup_errors.append(f"pre-cleanup list-queues failed: {exc}")

    removed_tables = 0
    for t in sorted(created_tables):
        if not t.startswith(ns.prefix):
            cleanup_errors.append(f"Refusing to delete table outside run namespace: {t!r}")
            continue
        res, _ = run_cmd_fn(
            [aws_bin, "dynamodb", "delete-table", "--table-name", t, "--endpoint-url", dynamo_endpoint],
            env,
            go_bin,
            aws_bin,
            check=False,
        )
        if res.returncode == 0:
            removed_tables += 1
        else:
            detail = (res.stderr or res.stdout or "").strip()
            cleanup_errors.append(f"delete-table {t} failed (rc={res.returncode}): {detail}")

    for b in sorted(created_buckets):
        if not b.startswith(ns.prefix):
            cleanup_errors.append(f"Refusing to delete bucket outside run namespace: {b!r}")
            continue
        res, _ = run_cmd_fn(
            [aws_bin, "s3", "rb", f"s3://{b}", "--force", "--endpoint-url", localstack_endpoint],
            env,
            go_bin,
            aws_bin,
            check=False,
        )
        if res.returncode != 0:
            detail = (res.stderr or res.stdout or "").strip()
            cleanup_errors.append(f"s3 rb s3://{b} failed (rc={res.returncode}): {detail}")

    for q_url in sorted(created_queue_urls):
        if f"/{ns.prefix}" not in q_url:
            cleanup_errors.append(f"Refusing to delete queue outside run namespace: {q_url!r}")
            continue
        res, _ = run_cmd_fn(
            [aws_bin, "sqs", "delete-queue", "--queue-url", q_url, "--endpoint-url", localstack_endpoint],
            env,
            go_bin,
            aws_bin,
            check=False,
        )
        if res.returncode != 0:
            detail = (res.stderr or res.stdout or "").strip()
            cleanup_errors.append(f"sqs delete-queue {q_url} failed (rc={res.returncode}): {detail}")

    # Verify all owned resources are gone.
    remaining: list[str] = []
    try:
        live_tables = set(list_tables_fn(aws_bin, env, dynamo_endpoint))
        for t in sorted(created_tables | {x for x in live_tables if x.startswith(ns.prefix)}):
            if t in live_tables:
                remaining.append(f"dynamodb:{t}")
    except Exception as exc:
        cleanup_errors.append(f"post-cleanup list-tables verification failed: {exc}")
        remaining.extend(f"dynamodb:{t} (unverified)" for t in sorted(created_tables))

    try:
        live_buckets = set(list_buckets_fn(aws_bin, env, localstack_endpoint))
        for b in sorted(created_buckets | {x for x in live_buckets if x.startswith(ns.prefix)}):
            if b in live_buckets:
                remaining.append(f"s3:{b}")
    except Exception as exc:
        cleanup_errors.append(f"post-cleanup list-buckets verification failed: {exc}")
        remaining.extend(f"s3:{b} (unverified)" for b in sorted(created_buckets))

    try:
        live_queues = set(list_queues_fn(aws_bin, env, localstack_endpoint))
        for q_url in sorted(created_queue_urls | {x for x in live_queues if f"/{ns.prefix}" in x}):
            if q_url in live_queues:
                remaining.append(f"sqs:{q_url}")
    except Exception as exc:
        cleanup_errors.append(f"post-cleanup list-queues verification failed: {exc}")
        remaining.extend(f"sqs:{q} (unverified)" for q in sorted(created_queue_urls))

    return removed_tables, cleanup_errors, remaining


def make_drill_env(
    ns: DrillNamespace,
    dynamo_endpoint: str,
    localstack_endpoint: str,
    api_port: int,
    go_bin: str,
    aws_bin: str,
) -> dict[str, str]:
    api_base = f"http://127.0.0.1:{api_port}"
    env = os.environ.copy()
    for blocked in (
        "AWS_PROFILE",
        "AWS_DEFAULT_PROFILE",
        "AWS_SESSION_TOKEN",
        "AWS_ROLE_ARN",
        "AWS_WEB_IDENTITY_TOKEN_FILE",
    ):
        env.pop(blocked, None)

    env["PATH"] = f"{os.path.dirname(go_bin)}:{os.path.dirname(aws_bin)}:{env.get('PATH', '')}"
    env.update(
        {
            "APP_ENV": "development",
            "AWS_REGION": "us-east-2",
            "AWS_DEFAULT_REGION": "us-east-2",
            "AWS_ACCESS_KEY_ID": "local",
            "AWS_SECRET_ACCESS_KEY": "local",
            "AWS_CONFIG_FILE": "/dev/null",
            "AWS_SHARED_CREDENTIALS_FILE": "/dev/null",
            "AWS_EC2_METADATA_DISABLED": "true",
            "AWS_PAGER": "",
            "DYNAMO_ENDPOINT": dynamo_endpoint,
            "S3_ENDPOINT": localstack_endpoint,
            "SQS_ENDPOINT": localstack_endpoint,
            "EMAIL_TRANSPORT": "log",
            "DYNAMO_TABLE_PREFIX": ns.prefix,
            "CATALOG_ASSET_BUCKET": ns.bucket,
            "CATALOG_BUILD_QUEUE_URL": f"{localstack_endpoint}/000000000000/{ns.catalog_queue}",
            "IMPORT_JOB_QUEUE_URL": f"{localstack_endpoint}/000000000000/{ns.import_queue}",
            "PUBLIC_BASE_URL": api_base,
            "AUTH_SECRET": "drill-only-placeholder-secret-0123456789abcdef",
            "PORT": str(api_port),
        }
    )
    validate_drill_isolation(dynamo_endpoint, localstack_endpoint, env, ns)
    return env


def create_single_test_table(
    aws_bin: str,
    go_bin: str,
    env: dict[str, str],
    dynamo_endpoint: str,
    table_name: str,
) -> None:
    run_cmd(
        [
            aws_bin,
            "dynamodb",
            "create-table",
            "--table-name",
            table_name,
            "--attribute-definitions",
            "AttributeName=id,AttributeType=S",
            "--key-schema",
            "AttributeName=id,KeyType=HASH",
            "--billing-mode",
            "PAY_PER_REQUEST",
            "--endpoint-url",
            dynamo_endpoint,
        ],
        env,
        go_bin,
        aws_bin,
    )


def finalize_cleanup_or_exit(
    cleanup_errors: list[str],
    remaining_resources: list[str],
    primary_error: BaseException | None = None,
) -> None:
    if not cleanup_errors and not remaining_resources:
        return
    msg_lines = ["Drill cleanup incomplete!"]
    if cleanup_errors:
        msg_lines.append(f"Cleanup errors ({len(cleanup_errors)}):")
        for err in cleanup_errors:
            msg_lines.append(f"  - {err}")
    if remaining_resources:
        msg_lines.append(f"Remaining owned resources ({len(remaining_resources)}): {remaining_resources}")
    if primary_error is not None:
        msg_lines.append(f"Primary error before cleanup: {primary_error}")
    full_msg = "\n".join(msg_lines)
    print(full_msg, file=sys.stderr)
    raise SystemExit(full_msg)


def run_self_tests(dynamo_endpoint: str, localstack_endpoint: str) -> None:
    print("=== Running Focused Rehearsal Script Checks (--self-test) ===")
    go_bin = resolve_binary("go")
    aws_bin = resolve_binary("aws")

    # -------------------------------------------------------------------------
    # Check 1: Occupied-port refusal before mutation + API process liveness check
    # -------------------------------------------------------------------------
    print("\n[Check 1/3] Occupied API port refusal before mutation & API process liveness check...")
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as occupied_sock:
        occupied_sock.bind(("127.0.0.1", 0))
        occupied_sock.listen(1)
        occupied_port = occupied_sock.getsockname()[1]

        ns_port = make_drill_namespace(f"selftest-port-{uuid.uuid4().hex[:6]}")
        env_port = make_drill_env(ns_port, dynamo_endpoint, localstack_endpoint, occupied_port, go_bin, aws_bin)

        cli_res = subprocess.run(
            [
                sys.executable,
                str(Path(__file__).resolve()),
                "--dynamo-endpoint",
                dynamo_endpoint,
                "--localstack-endpoint",
                localstack_endpoint,
                "--api-port",
                str(occupied_port),
                "--run-id",
                ns_port.run_id,
            ],
            cwd=REPO_ROOT,
            text=True,
            capture_output=True,
            check=False,
        )
        assert cli_res.returncode != 0, f"Expected nonzero exit when port {occupied_port} is occupied"
        assert f"Refusing occupied API port {occupied_port}" in (cli_res.stderr + cli_res.stdout), (
            f"Expected occupied-port refusal message, got: {cli_res.stderr}"
        )
        assert not discover_owned_tables(aws_bin, env_port, dynamo_endpoint, ns_port.prefix), (
            "Occupied-port refusal must happen before any table creation"
        )
        assert ns_port.bucket not in list_s3_buckets(aws_bin, env_port, localstack_endpoint), (
            "Occupied-port refusal must happen before any bucket creation"
        )
        assert not [q for q in list_sqs_queues(aws_bin, env_port, localstack_endpoint) if f"/{ns_port.prefix}" in q], (
            "Occupied-port refusal must happen before any queue creation"
        )

        # Verify dead API child process is caught before sending HTTP requests even while port is listening.
        exited_proc = subprocess.Popen(
            [sys.executable, "-c", "import sys; sys.stderr.write('simulated bind crash\\n'); sys.exit(1)"],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        exited_proc.wait(timeout=5)
        dead_caught = False
        try:
            wait_for_api_ready(exited_proc, f"http://127.0.0.1:{occupied_port}", max_attempts=2)
        except RuntimeError as exc:
            if "exited unexpectedly with code 1" in str(exc):
                dead_caught = True
            else:
                raise
        finally:
            terminate_process(exited_proc)
        assert dead_caught, "Expected wait_for_api_ready/http_req to refuse requests when launched API process exited"
    print("PASS: Occupied port refused (exit != 0) prior to mutation and exited API process caught before HTTP request.")

    # -------------------------------------------------------------------------
    # Check 2: Partial bootstrap failure tracks and cleans up created tables
    # -------------------------------------------------------------------------
    print("\n[Check 2/3] Partial bootstrap failure table tracking and cleanup...")
    ns_partial = make_drill_namespace(f"selftest-part-{uuid.uuid4().hex[:6]}")
    env_partial = make_drill_env(ns_partial, dynamo_endpoint, localstack_endpoint, 18081, go_bin, aws_bin)
    partial_tables: set[str] = set()
    partial_buckets: set[str] = set()
    partial_queues: set[str] = set()

    expected_partial_tables = {f"{ns_partial.prefix}users", f"{ns_partial.prefix}sessions"}

    def failing_bootstrap() -> None:
        for t_name in sorted(expected_partial_tables):
            create_single_test_table(aws_bin, go_bin, env_partial, dynamo_endpoint, t_name)
        raise RuntimeError("simulated partial bootstrap failure after creating 2 tables")

    bootstrap_failed = False
    try:
        bootstrap_drill_resources(
            ns=ns_partial,
            env=env_partial,
            go_bin=go_bin,
            aws_bin=aws_bin,
            dynamo_endpoint=dynamo_endpoint,
            localstack_endpoint=localstack_endpoint,
            created_tables=partial_tables,
            created_buckets=partial_buckets,
            created_queue_urls=partial_queues,
            bootstrap_fn=failing_bootstrap,
        )
    except RuntimeError as exc:
        if "simulated partial bootstrap failure" in str(exc):
            bootstrap_failed = True
        else:
            raise

    assert bootstrap_failed, "Expected failing_bootstrap to raise RuntimeError"
    assert partial_tables == expected_partial_tables, (
        f"Expected partial bootstrap tables {expected_partial_tables} to be tracked, got {partial_tables}"
    )

    removed_cnt, clean_errs, remaining = cleanup_and_verify_owned_resources(
        ns=ns_partial,
        created_tables=partial_tables,
        created_buckets=partial_buckets,
        created_queue_urls=partial_queues,
        aws_bin=aws_bin,
        go_bin=go_bin,
        env=env_partial,
        dynamo_endpoint=dynamo_endpoint,
        localstack_endpoint=localstack_endpoint,
    )
    assert removed_cnt == 2 and not clean_errs and not remaining, (
        f"Expected clean teardown of 2 partial tables; removed={removed_cnt}, errs={clean_errs}, remaining={remaining}"
    )
    print("PASS: Partial bootstrap tables were tracked, deleted, and verified gone.")

    # -------------------------------------------------------------------------
    # Check 3: Table-deletion failure aggregation and remaining-resource check
    # -------------------------------------------------------------------------
    print("\n[Check 3/3] Table-deletion failure aggregation and remaining resource reporting...")
    ns_delfail = make_drill_namespace(f"selftest-fail-{uuid.uuid4().hex[:6]}")
    env_delfail = make_drill_env(ns_delfail, dynamo_endpoint, localstack_endpoint, 18082, go_bin, aws_bin)
    t_fail = f"{ns_delfail.prefix}users"
    t_ok = f"{ns_delfail.prefix}sessions"
    delfail_tables: set[str] = set()
    try:
        create_single_test_table(aws_bin, go_bin, env_delfail, dynamo_endpoint, t_fail)
        delfail_tables.add(t_fail)
        create_single_test_table(aws_bin, go_bin, env_delfail, dynamo_endpoint, t_ok)
        delfail_tables.add(t_ok)

        def fault_injected_run_cmd(args: list[str], env: dict[str, str], go_b: str, aws_b: str, check: bool = False):
            if "delete-table" in args and t_fail in args:
                fake_proc = subprocess.CompletedProcess(
                    args=args,
                    returncode=255,
                    stdout="",
                    stderr=f"An error occurred (ResourceInUseException) when calling the DeleteTable operation: Table {t_fail} is in use",
                )
                return fake_proc, 0.001
            return run_cmd(args, env, go_b, aws_b, check=check)

        removed_cnt, clean_errs, remaining = cleanup_and_verify_owned_resources(
            ns=ns_delfail,
            created_tables=delfail_tables,
            created_buckets=set(),
            created_queue_urls=set(),
            aws_bin=aws_bin,
            go_bin=go_bin,
            env=env_delfail,
            dynamo_endpoint=dynamo_endpoint,
            localstack_endpoint=localstack_endpoint,
            run_cmd_fn=fault_injected_run_cmd,
        )
        assert removed_cnt == 1, f"Expected 1 table deleted despite failure on {t_fail}, got {removed_cnt}"
        assert len(clean_errs) == 1 and t_fail in clean_errs[0], f"Expected aggregated error for {t_fail}, got {clean_errs}"
        assert remaining == [f"dynamodb:{t_fail}"], f"Expected remaining=[dynamodb:{t_fail}], got {remaining}"

        exited_nonzero_with_names = False
        try:
            finalize_cleanup_or_exit(clean_errs, remaining)
        except SystemExit as exc:
            if exc.code and f"dynamodb:{t_fail}" in str(exc.code):
                exited_nonzero_with_names = True
            else:
                raise
        assert exited_nonzero_with_names, "Expected finalize_cleanup_or_exit to raise SystemExit listing remaining table"
    finally:
        # Clean up the remaining fault-injected table via namespace discovery and verify zero remain.
        _, real_errs, real_rem = cleanup_and_verify_owned_resources(
            ns=ns_delfail,
            created_tables=set(),
            created_buckets=set(),
            created_queue_urls=set(),
            aws_bin=aws_bin,
            go_bin=go_bin,
            env=env_delfail,
            dynamo_endpoint=dynamo_endpoint,
            localstack_endpoint=localstack_endpoint,
        )
        assert not real_errs and not real_rem, f"Self-test cleanup left resources: errs={real_errs}, rem={real_rem}"
    print("PASS: Table-deletion failure aggregated, remaining table reported in nonzero SystemExit, and final cleanup verified.")
    print("\n=== All 3 Focused Rehearsal Script Checks PASSED ===")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--dynamo-endpoint", default="http://127.0.0.1:8000", help="Loopback DynamoDB Local endpoint")
    parser.add_argument("--localstack-endpoint", default="http://127.0.0.1:4566", help="Loopback LocalStack endpoint")
    parser.add_argument("--api-port", type=int, default=18080, help="Local port for the temporary drill API server")
    parser.add_argument("--run-id", default=None, help="Optional unique per-run ID suffix for drill resources")
    parser.add_argument(
        "--self-test",
        action="store_true",
        help="Run focused local checks for occupied-port refusal, partial bootstrap failure, and table-deletion failure",
    )
    args = parser.parse_args()

    dynamo_endpoint = require_loopback_url("dynamo-endpoint", args.dynamo_endpoint)
    localstack_endpoint = require_loopback_url("localstack-endpoint", args.localstack_endpoint)

    if args.self_test:
        run_self_tests(dynamo_endpoint, localstack_endpoint)
        return

    ns = make_drill_namespace(args.run_id)
    api_port = int(args.api_port)
    api_base = f"http://127.0.0.1:{api_port}"

    go_bin = resolve_binary("go")
    aws_bin = resolve_binary("aws")
    env = make_drill_env(ns, dynamo_endpoint, localstack_endpoint, api_port, go_bin, aws_bin)

    # Refuse an occupied API port before any resource creation or state mutation.
    ensure_api_port_available(api_port)

    # Verify no resources in this per-run namespace pre-exist.
    existing_tables = set(list_dynamo_tables(aws_bin, env, dynamo_endpoint))
    preexisting_drill_tables = [t for t in existing_tables if t.startswith(ns.prefix)]
    if preexisting_drill_tables:
        raise RuntimeError(f"Refusing to run: drill tables already exist for {ns.prefix}: {preexisting_drill_tables}")

    pre_buckets = list_s3_buckets(aws_bin, env, localstack_endpoint)
    if ns.bucket in pre_buckets:
        raise RuntimeError(f"Refusing to run: drill bucket {ns.bucket!r} already exists")

    pre_queues = list_sqs_queues(aws_bin, env, localstack_endpoint)
    for q in ns.queues:
        if any(url.endswith(f"/{q}") for url in pre_queues):
            raise RuntimeError(f"Refusing to run: drill queue {q!r} already exists")

    pre_local_counts = get_local_prefix_counts(aws_bin, env, dynamo_endpoint)
    print(f"Per-run drill namespace: prefix={ns.prefix!r}, bucket={ns.bucket!r}")
    print(
        f"Pre-drill baseline counts: micup-local-* table item counts={pre_local_counts}, "
        f"bucket_count={len(pre_buckets)}, queue_count={len(pre_queues)}"
    )

    created_tables: set[str] = set()
    created_buckets: set[str] = set()
    created_queue_urls: set[str] = set()

    api_proc: subprocess.Popen | None = None
    import_worker_proc: subprocess.Popen | None = None
    catalog_worker_proc: subprocess.Popen | None = None

    with tempfile.TemporaryDirectory(prefix="micup-m9-drill-") as tmp_dir:
        tmp_path = Path(tmp_dir)
        api_bin = str(tmp_path / "micup-server")
        import_worker_bin = str(tmp_path / "import-worker")
        catalog_worker_bin = str(tmp_path / "catalog-worker")

        run_cmd([go_bin, "build", "-o", api_bin, "."], env, go_bin, aws_bin)
        run_cmd([go_bin, "build", "-o", import_worker_bin, "./cmd/import-worker"], env, go_bin, aws_bin)
        run_cmd([go_bin, "build", "-o", catalog_worker_bin, "./cmd/catalog-worker"], env, go_bin, aws_bin)

        # Re-check API port availability right before mutating local emulator state.
        ensure_api_port_available(api_port)

        primary_error: BaseException | None = None
        try:
            print(f"\n--- Setting up isolated drill resources (namespace {ns.prefix}) ---")
            t_setup = time.monotonic()
            bootstrap_drill_resources(
                ns=ns,
                env=env,
                go_bin=go_bin,
                aws_bin=aws_bin,
                dynamo_endpoint=dynamo_endpoint,
                localstack_endpoint=localstack_endpoint,
                created_tables=created_tables,
                created_buckets=created_buckets,
                created_queue_urls=created_queue_urls,
            )
            assert len(created_tables) == len(TABLE_SUFFIXES), (
                f"Expected {len(TABLE_SUFFIXES)} drill tables, tracked {len(created_tables)}"
            )
            print(f"Drill resource setup completed in {time.monotonic() - t_setup:.3f}s ({len(created_tables)} tables)")

            print("\n--- Starting API server and workers against drill namespace ---")
            ensure_api_port_available(api_port)
            api_proc = subprocess.Popen(
                [api_bin],
                cwd=REPO_ROOT,
                env=env,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
            )
            import_worker_proc = subprocess.Popen(
                [import_worker_bin, "-poll"],
                cwd=REPO_ROOT,
                env=env,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
            )
            catalog_worker_proc = subprocess.Popen(
                [catalog_worker_bin, "-poll"],
                cwd=REPO_ROOT,
                env=env,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
            )

            wait_for_api_ready(api_proc, api_base)
            ensure_process_alive(import_worker_proc, "import-worker")
            ensure_process_alive(catalog_worker_proc, "catalog-worker")

            def api_call(
                method: str,
                path: str,
                body: dict | list | str | bytes | None = None,
                headers: dict[str, str] | None = None,
            ) -> tuple[int, dict[str, str], dict | list | str]:
                return http_req(api_base, method, path, body=body, headers=headers, api_proc=api_proc)

            # =================================================================
            # PART 1: CURRENT-VERSION SMOKE FLOW
            # =================================================================
            t_smoke_start = time.monotonic()
            print("\n=== PART 1: Current-Version Smoke Flow ===")

            st, _, reg = api_call(
                "POST",
                "/api/v1/auth/register",
                {
                    "email": "drill-host@example.invalid",
                    "password": "DrillOnlyPassword123!",
                    "display_name": "Drill Host",
                },
            )
            assert st == 201 and isinstance(reg, dict), f"register failed: {st}"
            auth_hdr = {"Authorization": f"Bearer {reg['access_token']}"}
            user_id = reg["user"]["id"]
            print(f"1. Registered host user_id={user_id} (status={st})")

            st, _, sb = api_call(
                "POST",
                "/api/v1/songbooks",
                {
                    "name": "Drill Smoke Songbook",
                    "slug": "drill-smoke-book",
                    "is_public": True,
                },
                headers=auth_hdr,
            )
            assert st == 201 and isinstance(sb, dict), f"create songbook failed: {st}"
            sb_id = sb["id"]
            sb_pub_id = sb["public_id"]
            print(f"2. Created public songbook id={sb_id} public_id={sb_pub_id} slug={sb['slug']} (status={st})")

            st, _, imp = api_call(
                "POST",
                f"/api/v1/songbooks/{sb_id}/import-jobs",
                {
                    "prune_missing": False,
                    "songs": [
                        {
                            "artist": "Queen",
                            "title": "Bohemian Rhapsody",
                            "disc": "SC8101-01",
                            "fingerprint": "fp-queen-bohemian",
                        },
                        {
                            "artist": "Journey",
                            "title": "Don't Stop Believin'",
                            "disc": "SC8102-04",
                            "fingerprint": "fp-journey-believin",
                        },
                        {
                            "artist": "ABBA",
                            "title": "Dancing Queen",
                            "disc": "SF100-02",
                            "fingerprint": "fp-abba-dancing",
                        },
                    ],
                },
                headers=auth_hdr,
            )
            assert st in (200, 201, 202) and isinstance(imp, dict), f"create import failed: {st}"
            imp_id = imp["id"]
            print(
                f"3. Submitted import job id={imp_id} status={imp['status']} "
                f"generation={imp.get('generation')} (HTTP {st})"
            )

            t_imp = time.monotonic()
            for _ in range(60):
                ensure_process_alive(import_worker_proc, "import-worker")
                ensure_process_alive(catalog_worker_proc, "catalog-worker")
                st, _, job_state = api_call("GET", f"/api/v1/songbooks/{sb_id}/import-jobs/{imp_id}", headers=auth_hdr)
                if isinstance(job_state, dict) and job_state.get("status") == "completed":
                    break
                time.sleep(0.25)
            else:
                raise RuntimeError(f"Import did not complete in time: {job_state}")
            print(
                f"4. Import completed in {time.monotonic() - t_imp:.3f}s: "
                f"processed={job_state['processed_songs']} status={job_state['status']}"
            )

            st, _, pub_man = api_call("GET", f"/api/v1/songbooks/{sb_pub_id}/public-manifest")
            assert st == 200 and isinstance(pub_man, dict) and pub_man.get("static_ready") is True, (
                f"public manifest failed: {st} {pub_man}"
            )
            st, _, sb_info = api_call("GET", f"/api/v1/songbooks/{sb_id}", headers=auth_hdr)
            assert isinstance(sb_info, dict)
            cat_ver_id = sb_info["catalog_version_id"]
            s3_res, _ = run_cmd(
                [
                    aws_bin,
                    "s3",
                    "ls",
                    f"s3://{ns.bucket}/catalogs/epochs/{cat_ver_id}/",
                    "--recursive",
                    "--endpoint-url",
                    localstack_endpoint,
                ],
                env,
                go_bin,
                aws_bin,
            )
            print(
                f"5. Public songbook serving catalog_version_id={cat_ver_id} song_count={sb_info['song_count']}; "
                f"S3 epoch objects:\n{s3_res.stdout.strip()}"
            )

            st, _, ev = api_call(
                "POST",
                "/api/v1/events",
                {
                    "name": "Drill Live Show",
                    "songbook_id": sb_id,
                    "auto_accept": False,
                    "allow_key_change": True,
                    "max_active_per_singer": 5,
                    "rotation_mode": "round_robin",
                },
                headers=auth_hdr,
            )
            assert st == 201 and isinstance(ev, dict), f"create event failed: {st}"
            ev_id = ev["id"]
            join_code = ev["join_code"]

            st, _, joined = api_call("GET", f"/api/v1/join/{join_code}")
            assert st == 200 and isinstance(joined, dict) and joined["id"] == ev_id, f"join lookup failed: {st}"
            print(f"6. Created event id={ev_id} join_code={join_code} and verified public join lookup (status={st})")

            st, _, r1 = api_call(
                "POST",
                f"/api/v1/events/{ev_id}/requests",
                {"singer_name": "Singer One", "song_title": "Bohemian Rhapsody", "song_artist": "Queen", "key_change": 0},
            )
            assert st == 201 and isinstance(r1, dict), f"r1 failed: {st}"
            r1_id, r1_tok = r1["id"], r1["cancel_token"]

            st, _, r2 = api_call(
                "POST",
                f"/api/v1/events/{ev_id}/requests",
                {
                    "singer_name": "Singer Two",
                    "song_title": "Don't Stop Believin'",
                    "song_artist": "Journey",
                    "key_change": -1,
                },
            )
            assert st == 201 and isinstance(r2, dict), f"r2 failed: {st}"
            r2_id = r2["id"]

            st, _, r3 = api_call(
                "POST",
                f"/api/v1/events/{ev_id}/requests",
                {"singer_name": "Singer Three", "song_title": "Dancing Queen", "song_artist": "ABBA", "key_change": 2},
            )
            assert st == 201 and isinstance(r3, dict), f"r3 failed: {st}"
            r3_id = r3["id"]

            st, _, _ = api_call(
                "DELETE", f"/api/v1/events/{ev_id}/requests/{r1_id}", headers={"X-Cancel-Token": r1_tok}
            )
            assert st == 204, f"cancel r1 failed: {st}"

            st, _, q_state = api_call("GET", f"/api/v1/events/{ev_id}/requests", headers=auth_hdr)
            assert isinstance(q_state, dict)
            qv = q_state["queue_version"]

            st, _, _ = api_call(
                "PATCH",
                f"/api/v1/events/{ev_id}/requests/{r2_id}",
                {"status": "accepted", "expected_queue_version": qv},
                headers=auth_hdr,
            )
            assert st == 200, f"accept r2 failed: {st}"
            qv += 1

            st, _, _ = api_call(
                "PATCH",
                f"/api/v1/events/{ev_id}/requests/{r3_id}",
                {"status": "accepted", "expected_queue_version": qv},
                headers=auth_hdr,
            )
            assert st == 200, f"accept r3 failed: {st}"
            qv += 1

            st, _, _ = api_call(
                "POST",
                f"/api/v1/events/{ev_id}/requests/reorder",
                {"order": [r3_id, r2_id], "expected_queue_version": qv},
                headers=auth_hdr,
            )
            assert st == 200, f"reorder failed: {st}"
            qv += 1

            st, _, _ = api_call(
                "PATCH",
                f"/api/v1/events/{ev_id}/requests/{r3_id}",
                {"status": "completed", "expected_queue_version": qv},
                headers=auth_hdr,
            )
            assert st == 200, f"complete r3 failed: {st}"

            st_200, hdrs_200, _ = api_call("GET", f"/api/v1/events/{ev_id}/queue?since=0")
            exposed_qv = hdrs_200.get("X-Queue-Version") or hdrs_200.get("x-queue-version")
            st_204, _, _ = api_call("GET", f"/api/v1/events/{ev_id}/queue?since={exposed_qv}")
            assert st_200 == 200 and st_204 == 204, f"polling check failed: {st_200}, {st_204}"
            print(
                f"7. Request flow & conditional polling verified: queue_version={exposed_qv} "
                f"(GET /queue?since=0 -> {st_200}, GET /queue?since={exposed_qv} -> {st_204})"
            )
            print(f"Smoke flow total duration: {time.monotonic() - t_smoke_start:.3f}s")

            # =================================================================
            # PART 2: M8 OPERATOR RECOVERY DRILL (cmd/import-reconciler)
            # =================================================================
            print("\n=== PART 2: M8 Operator Recovery Drill (cmd/import-reconciler) ===")
            t_m8_start = time.monotonic()
            exhausted_job_id = "job-m8-drill-exhausted"
            exhausted_manifest_key = f"imports/{exhausted_job_id}.json"

            manifest_file = tmp_path / "m8_drill_manifest.json"
            manifest_file.write_text(
                json.dumps(
                    {
                        "prune_missing": False,
                        "songs": [
                            {
                                "artist": "Toto",
                                "title": "Africa",
                                "disc": "SC8200-01",
                                "fingerprint": "fp-toto-africa",
                                "is_available": True,
                            },
                            {
                                "artist": "Bon Jovi",
                                "title": "Livin' on a Prayer",
                                "disc": "SC8200-02",
                                "fingerprint": "fp-bonjovi-prayer",
                                "is_available": True,
                            },
                        ],
                    }
                ),
                encoding="utf-8",
            )

            run_cmd(
                [
                    aws_bin,
                    "s3api",
                    "put-object",
                    "--bucket",
                    ns.bucket,
                    "--key",
                    exhausted_manifest_key,
                    "--body",
                    str(manifest_file),
                    "--endpoint-url",
                    localstack_endpoint,
                ],
                env,
                go_bin,
                aws_bin,
            )

            now_iso = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
            item_json = json.dumps(
                {
                    "id": {"S": exhausted_job_id},
                    "songbook_id": {"S": sb_id},
                    "manifest_key": {"S": exhausted_manifest_key},
                    "status": {"S": "queued"},
                    "dispatch_ready": {"BOOL": True},
                    "dispatch_confirmed": {"BOOL": False},
                    "dispatch_attempts": {"N": "5"},
                    "generation": {"N": "2"},
                    "total_songs": {"N": "2"},
                    "processed_songs": {"N": "0"},
                    "error": {"S": "simulated SQS timeout after 5 dispatch attempts"},
                    "created_at": {"S": now_iso},
                    "updated_at": {"S": now_iso},
                }
            )
            run_cmd(
                [
                    aws_bin,
                    "dynamodb",
                    "put-item",
                    "--table-name",
                    f"{ns.prefix}import-jobs",
                    "--item",
                    item_json,
                    "--endpoint-url",
                    dynamo_endpoint,
                ],
                env,
                go_bin,
                aws_bin,
            )

            run_cmd(
                [go_bin, "run", "./cmd/import-reconciler"],
                env,
                go_bin,
                aws_bin,
                label="M8 Step 1: Reconciler detects exhausted job alert",
            )
            run_cmd(
                [go_bin, "run", "./cmd/import-reconciler", "--list-exhausted"],
                env,
                go_bin,
                aws_bin,
                label="M8 Step 2: Operator lists exhausted jobs",
            )
            run_cmd(
                [go_bin, "run", "./cmd/import-reconciler", "--reset-job", exhausted_job_id],
                env,
                go_bin,
                aws_bin,
                label="M8 Step 3: Operator resets exhausted job",
            )
            run_cmd(
                [go_bin, "run", "./cmd/import-reconciler"],
                env,
                go_bin,
                aws_bin,
                label="M8 Step 4: Reconciler dispatches reset job",
            )

            for _ in range(60):
                ensure_process_alive(import_worker_proc, "import-worker")
                ensure_process_alive(catalog_worker_proc, "catalog-worker")
                st, _, j_after = api_call(
                    "GET", f"/api/v1/songbooks/{sb_id}/import-jobs/{exhausted_job_id}", headers=auth_hdr
                )
                if isinstance(j_after, dict) and j_after.get("status") == "completed":
                    break
                time.sleep(0.25)
            else:
                raise RuntimeError(f"Reset job did not complete: {j_after}")

            terminate_process(import_worker_proc)
            terminate_process(catalog_worker_proc)
            import_worker_proc = None
            catalog_worker_proc = None

            st, _, sb_after = api_call("GET", f"/api/v1/songbooks/{sb_id}", headers=auth_hdr)
            assert isinstance(sb_after, dict)
            print(
                f"M8 recovery verified in {time.monotonic() - t_m8_start:.3f}s: "
                f"job={exhausted_job_id} status={j_after['status']} "
                f"songbook_songs={sb_after['song_count']} catalog={sb_after['catalog_version_id']}"
            )

            # =================================================================
            # PART 3: M14 OPERATOR RECOVERY DRILL (cmd/catalog-gc)
            # =================================================================
            print("\n=== PART 3: M14 Operator Recovery Drill (cmd/catalog-gc) ===")
            t_m14_start = time.monotonic()
            interrupted_cat_id = "cat_interrupted_m14_drill"
            interrupted_prefix = f"catalogs/epochs/{interrupted_cat_id}/epoch_stale_1"
            orphan_prefix = "catalogs/epochs/cat_orphan_m14_drill/epoch_orphan_1"

            for key in (
                f"{interrupted_prefix}/manifest.json.gz",
                f"{interrupted_prefix}/search.json.gz",
                f"{orphan_prefix}/manifest.json.gz",
            ):
                run_cmd(
                    [
                        aws_bin,
                        "s3api",
                        "put-object",
                        "--bucket",
                        ns.bucket,
                        "--key",
                        key,
                        "--body",
                        str(manifest_file),
                        "--endpoint-url",
                        localstack_endpoint,
                    ],
                    env,
                    go_bin,
                    aws_bin,
                )

            past_created = "2026-10-10T00:00:00Z"
            past_expired = "2026-10-16T00:00:00Z"
            cat_item_json = json.dumps(
                {
                    "id": {"S": interrupted_cat_id},
                    "schema_version": {"N": "1"},
                    "status": {"S": "deleting"},
                    "song_count": {"N": "3"},
                    "asset_prefix": {"S": interrupted_prefix},
                    "lease_token": {"S": "expired-lease-token-123"},
                    "lease_expires_at": {"S": past_expired},
                    "created_at": {"S": past_created},
                    "updated_at": {"S": past_expired},
                }
            )
            run_cmd(
                [
                    aws_bin,
                    "dynamodb",
                    "put-item",
                    "--table-name",
                    f"{ns.prefix}catalog-versions",
                    "--item",
                    cat_item_json,
                    "--endpoint-url",
                    dynamo_endpoint,
                ],
                env,
                go_bin,
                aws_bin,
            )

            run_cmd(
                [go_bin, "run", "./cmd/catalog-gc", "--dry-run=true", "--grace=0s"],
                env,
                go_bin,
                aws_bin,
                label="M14 Step 1: catalog-gc dry-run=true",
            )
            run_cmd(
                [go_bin, "run", "./cmd/catalog-gc", "--dry-run=false", "--grace=0s"],
                env,
                go_bin,
                aws_bin,
                label="M14 Step 2: catalog-gc dry-run=false",
            )

            s3_after_gc, _ = run_cmd(
                [
                    aws_bin,
                    "s3",
                    "ls",
                    f"s3://{ns.bucket}/catalogs/",
                    "--recursive",
                    "--endpoint-url",
                    localstack_endpoint,
                ],
                env,
                go_bin,
                aws_bin,
            )
            assert interrupted_cat_id not in s3_after_gc.stdout, "interrupted catalog assets were not purged"
            assert "cat_orphan_m14_drill" not in s3_after_gc.stdout, "orphan prefix assets were not purged"
            assert sb_after["catalog_version_id"] in s3_after_gc.stdout, "live referenced catalog assets were missing"
            print(
                f"M14 recovery verified in {time.monotonic() - t_m14_start:.3f}s: "
                f"interrupted & orphan prefixes purged; live catalog {sb_after['catalog_version_id']} intact."
            )

            # =================================================================
            # PART 4: M7 OPERATOR RECOVERY DRILL (cmd/catalog-gc account recovery)
            # =================================================================
            print("\n=== PART 4: M7 Operator Recovery Drill (cmd/catalog-gc account deletion recovery) ===")
            t_m7_start = time.monotonic()

            st, _, uA = api_call(
                "POST",
                "/api/v1/auth/register",
                {
                    "email": "drill-delete-a@example.invalid",
                    "password": "DrillDeletePassword123!",
                    "display_name": "Deleting Host A",
                },
            )
            assert st == 201 and isinstance(uA, dict)
            uA_id, uA_tok = uA["user"]["id"], uA["access_token"]
            st, _, sbA = api_call(
                "POST",
                "/api/v1/songbooks",
                {"name": "Host A Book", "is_public": True},
                headers={"Authorization": f"Bearer {uA_tok}"},
            )
            assert st == 201 and isinstance(sbA, dict)
            st, _, _ = api_call(
                "POST",
                "/api/v1/events",
                {"name": "Host A Show", "songbook_id": sbA["id"]},
                headers={"Authorization": f"Bearer {uA_tok}"},
            )
            assert st == 201

            st, _, uB = api_call(
                "POST",
                "/api/v1/auth/register",
                {
                    "email": "drill-delete-b@example.invalid",
                    "password": "DrillDeletePassword123!",
                    "display_name": "Deleting Host B",
                },
            )
            assert st == 201 and isinstance(uB, dict)
            uB_id, uB_tok = uB["user"]["id"], uB["access_token"]
            st, _, _ = api_call(
                "POST",
                "/api/v1/songbooks",
                {"name": "Host B Book", "is_public": True},
                headers={"Authorization": f"Bearer {uB_tok}"},
            )
            assert st == 201

            for uid, step_name, err_str in (
                (uA_id, "delete_events", "simulated DynamoDB timeout during delete_events"),
                (uB_id, "delete_songbooks", "simulated S3 timeout during delete_songbooks"),
            ):
                run_cmd(
                    [
                        aws_bin,
                        "dynamodb",
                        "update-item",
                        "--table-name",
                        f"{ns.prefix}users",
                        "--key",
                        json.dumps({"id": {"S": uid}}),
                        "--update-expression",
                        (
                            "SET deleting = :t, deletion_epoch = :de, deletion_auth_epoch = :dae, "
                            "auth_epoch = :ae, deletion_started_at = :now, deletion_step = :step, "
                            "deletion_error = :err REMOVE password_hash"
                        ),
                        "--expression-attribute-values",
                        json.dumps(
                            {
                                ":t": {"BOOL": True},
                                ":de": {"S": f"del-epoch-{uid[:8]}"},
                                ":dae": {"S": ""},
                                ":ae": {"S": f"rotated-epoch-{uid[:8]}"},
                                ":now": {"S": now_iso},
                                ":step": {"S": step_name},
                                ":err": {"S": err_str},
                            }
                        ),
                        "--endpoint-url",
                        dynamo_endpoint,
                    ],
                    env,
                    go_bin,
                    aws_bin,
                )

            st_auth_a, _, _ = api_call(
                "GET", "/api/v1/auth/me", headers={"Authorization": f"Bearer {uA_tok}"}
            )
            assert st_auth_a == 401, f"expected 401 for deleting user A, got {st_auth_a}"
            print(f"Verified normal bearer auth rejected for deleting account ({st_auth_a} Unauthorized)")

            run_cmd(
                [go_bin, "run", "./cmd/catalog-gc", "-list-deleting-accounts"],
                env,
                go_bin,
                aws_bin,
                label="M7 Step 1: List deleting accounts",
            )
            run_cmd(
                [go_bin, "run", "./cmd/catalog-gc", "-recover-account", uA_id],
                env,
                go_bin,
                aws_bin,
                label=f"M7 Step 2: Recover specific account ({uA_id})",
            )
            run_cmd(
                [go_bin, "run", "./cmd/catalog-gc", "-recover-deleting-accounts"],
                env,
                go_bin,
                aws_bin,
                label="M7 Step 3: Recover all remaining deleting accounts",
            )
            run_cmd(
                [go_bin, "run", "./cmd/catalog-gc", "-list-deleting-accounts"],
                env,
                go_bin,
                aws_bin,
                label="M7 Step 4: Verify zero deleting accounts remain",
            )

            st_rereg, _, _ = api_call(
                "POST",
                "/api/v1/auth/register",
                {
                    "email": "drill-delete-a@example.invalid",
                    "password": "DrillNewPassword123!",
                    "display_name": "Re-registered Host A",
                },
            )
            assert st_rereg == 201, f"expected 201 re-registering released email, got {st_rereg}"
            print(
                f"M7 recovery verified in {time.monotonic() - t_m7_start:.3f}s: "
                f"both deleting accounts purged and email re-registration returned {st_rereg}."
            )

        except BaseException as exc:
            primary_error = exc
            raise
        finally:
            for p in (import_worker_proc, catalog_worker_proc, api_proc):
                terminate_process(p)

            print(f"\n--- Cleaning up tracked drill resources (namespace {ns.prefix}) ---")
            t_clean = time.monotonic()
            removed_tables, cleanup_errors, remaining_resources = cleanup_and_verify_owned_resources(
                ns=ns,
                created_tables=created_tables,
                created_buckets=created_buckets,
                created_queue_urls=created_queue_urls,
                aws_bin=aws_bin,
                go_bin=go_bin,
                env=env,
                dynamo_endpoint=dynamo_endpoint,
                localstack_endpoint=localstack_endpoint,
            )
            print(
                f"Drill resource cleanup completed in {time.monotonic() - t_clean:.3f}s "
                f"(removed {removed_tables} drill tables, remaining={len(remaining_resources)})"
            )
            finalize_cleanup_or_exit(cleanup_errors, remaining_resources, primary_error)

    post_local_counts = get_local_prefix_counts(aws_bin, env, dynamo_endpoint)
    post_buckets = list_s3_buckets(aws_bin, env, localstack_endpoint)
    post_queues = list_sqs_queues(aws_bin, env, localstack_endpoint)
    assert pre_local_counts == post_local_counts, (
        f"micup-local-* table item counts changed! pre={pre_local_counts} post={post_local_counts}"
    )
    assert len(pre_buckets) == len(post_buckets) and set(pre_buckets) == set(post_buckets), (
        f"Bucket list changed! pre={pre_buckets} post={post_buckets}"
    )
    assert len(pre_queues) == len(post_queues) and set(pre_queues) == set(post_queues), (
        f"Queue list changed! pre={pre_queues} post={post_queues}"
    )
    print(
        f"\nPost-drill count check PASSED: micup-local-* table item counts, bucket count ({len(post_buckets)}), "
        f"and queue count ({len(post_queues)}) counts unchanged."
    )


if __name__ == "__main__":
    main()
