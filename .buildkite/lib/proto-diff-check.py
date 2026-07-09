#!/usr/bin/env python3
from __future__ import annotations

import argparse
import difflib
import json
import os
import sys
import urllib.parse
import urllib.request

MAX_SLACK_DIFF_CHARS = 30_000
DIFF_ARTIFACT = "proto-sync/rbln_services.diff"


def _github(repo: str, path: str, accept: str) -> bytes:
    token = os.environ.get("GIT_PAT", "").strip()
    if not token:
        sys.exit("error: GIT_PAT is not set")
    req = urllib.request.Request(
        f"https://api.github.com/repos/{repo}/{path}",
        headers={"Authorization": f"Bearer {token}", "Accept": accept},
    )
    with urllib.request.urlopen(req) as resp:
        return resp.read()


def _normalize(text: str) -> list[str]:
    return [
        line for line in text.splitlines(keepends=True)
        if line.strip() and not line.strip().startswith("option ")
    ]


def _check(repo: str, ref: str, source_path: str, local_path: str) -> tuple[int, str]:
    quoted_ref = urllib.parse.quote(ref, safe="")
    remote = _github(
        repo,
        f"contents/{urllib.parse.quote(source_path)}?ref={quoted_ref}",
        "application/vnd.github.raw+json",
    ).decode()
    source_sha = json.loads(
        _github(repo, f"commits/{quoted_ref}", "application/vnd.github+json")
    )["sha"][:8]
    source = f"{repo}@{source_sha} {source_path}"

    local = open(local_path, encoding="utf-8").read()
    diff = "".join(difflib.unified_diff(
        _normalize(remote), _normalize(local),
        fromfile=source, tofile=local_path,
    ))

    if not diff:
        print(f"OK: {local_path} is in sync with {source}", flush=True)
        return 0, f":white_check_mark: 변경사항 없음 — `{local_path}` ↔ `{source}`"

    os.makedirs(os.path.dirname(DIFF_ARTIFACT), exist_ok=True)
    with open(DIFF_ARTIFACT, "w", encoding="utf-8") as f:
        f.write(diff)
    print(diff, end="", flush=True)

    snippet = diff
    if len(snippet) > MAX_SLACK_DIFF_CHARS:
        snippet = (snippet[:MAX_SLACK_DIFF_CHARS].rsplit("\n", 1)[0]
                   + "\n... (truncated; full diff in build artifacts)")
    build_url = os.environ.get("BUILDKITE_BUILD_URL", "")
    link = f"<{build_url}|Buildkite build>\n" if build_url else ""
    return 1, (f":rotating_light: 변경 사항 있음 — `{local_path}` ↔ `{source}`\n"
               f"{link}```{snippet}```")


def _post(channel: str, text: str, token: str) -> None:
    body = json.dumps({"channel": channel, "text": text}).encode()
    req = urllib.request.Request("https://slack.com/api/chat.postMessage", data=body, method="POST")
    req.add_header("Authorization", f"Bearer {token}")
    req.add_header("Content-Type", "application/json; charset=utf-8")
    with urllib.request.urlopen(req) as resp:
        out = json.loads(resp.read())
    if not out.get("ok"):
        sys.exit(f"error: slack chat.postMessage failed: {out.get('error')}")
    print(f"[slack] posted to {channel}", flush=True)


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--source-repo",
                   default=os.environ.get("SOURCE_REPO", "rebellions-sw/ssw-common-tools"))
    p.add_argument("--source-ref", default=os.environ.get("SOURCE_REF", "dev"))
    p.add_argument("--source-path",
                   default=os.environ.get("SOURCE_PATH", "rbln-smd/proto/public/rbln_smd.proto"))
    p.add_argument("--local-path",
                   default=os.environ.get("LOCAL_PATH", "api/rbln_services.proto"))
    p.add_argument("--channel", default=os.environ.get("SLACK_CHANNEL_ID", ""))
    p.add_argument("--print", dest="print_only", action="store_true",
                   help="print the Slack message instead of posting")
    a = p.parse_args(argv)

    token = os.environ.get("SLACK_OAUTH_TOKEN", "").strip()
    if not a.print_only:
        if not token:
            sys.exit("error: SLACK_OAUTH_TOKEN is not set")
        if not a.channel:
            sys.exit("error: no Slack channel (--channel or SLACK_CHANNEL_ID)")

    try:
        rc, text = _check(a.source_repo, a.source_ref, a.source_path, a.local_path)
    except Exception as exc:
        build_url = os.environ.get("BUILDKITE_BUILD_URL", "")
        crash = f":x: proto diff check failed to run: `{exc}`\n{build_url}"
        print(crash) if a.print_only else _post(a.channel, crash, token)
        raise

    print(text) if a.print_only else _post(a.channel, text, token)
    return rc


if __name__ == "__main__":
    sys.exit(main())
