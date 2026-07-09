#!/usr/bin/env python3
from __future__ import annotations

import argparse
import difflib
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request

PRODUCT = "Proto Diff Check"
COLOR_PASS = "#36a64f"
COLOR_FAIL = "#FF0000"
MAX_SLACK_DIFF_CHARS = 30_000
BLOCK_CHUNK_CHARS = 2_900
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


def _latest_tag(repo: str) -> str:
    tags = json.loads(_github(repo, "tags?per_page=100", "application/vnd.github+json"))
    stable = [t["name"] for t in tags if re.fullmatch(r"v\d+\.\d+\.\d+", t["name"])]
    if not stable:
        sys.exit(f"error: no stable vX.Y.Z tags found in {repo}")
    return max(stable, key=lambda n: tuple(map(int, n[1:].split("."))))


def _normalize(text: str) -> list[str]:
    return [
        line for line in text.splitlines(keepends=True)
        if line.strip() and not line.strip().startswith("option ")
    ]


def _section(text: str) -> dict:
    return {"type": "section", "text": {"type": "mrkdwn", "text": text}}


def _context(text: str) -> dict:
    return {"type": "context", "elements": [{"type": "mrkdwn", "text": text}]}


def _title(emoji: str) -> str:
    build_url = os.environ.get("BUILDKITE_BUILD_URL", "")
    build_no = os.environ.get("BUILDKITE_BUILD_NUMBER", "")
    if build_url and build_no:
        return f"{emoji} *{PRODUCT} <{build_url}|#{build_no}>*"
    return f"{emoji} *{PRODUCT}*"


def _message(text: str, blocks: list[dict], color: str) -> dict:
    return {
        "attachments": [{"fallback": text, "color": color, "blocks": blocks}],
    }


def _diff_blocks(diff: str) -> list[dict]:
    if len(diff) > MAX_SLACK_DIFF_CHARS:
        diff = (diff[:MAX_SLACK_DIFF_CHARS].rsplit("\n", 1)[0]
                + "\n... (truncated; full diff in build artifacts)")
    blocks, chunk = [], ""
    for line in diff.splitlines(keepends=True):
        if len(chunk) + len(line) > BLOCK_CHUNK_CHARS:
            blocks.append(_section(f"```{chunk}```"))
            chunk = ""
        chunk += line
    if chunk:
        blocks.append(_section(f"```{chunk}```"))
    return blocks


def _check(repo: str, ref: str, source_path: str, local_path: str) -> tuple[int, dict]:
    if ref == "latest-tag":
        ref = _latest_tag(repo)
    quoted_ref = urllib.parse.quote(ref, safe="")
    try:
        remote = _github(
            repo,
            f"contents/{urllib.parse.quote(source_path)}?ref={quoted_ref}",
            "application/vnd.github.raw+json",
        ).decode()
    except urllib.error.HTTPError as e:
        if e.code == 404:
            raise RuntimeError(f"{source_path} not found in {repo}@{ref}") from None
        raise
    source_sha = json.loads(
        _github(repo, f"commits/{quoted_ref}", "application/vnd.github+json")
    )["sha"][:8]
    source = f"{repo}@{ref} ({source_sha}) {source_path}"
    compared = _context(f"`{local_path}` ↔ `{source}`")

    local = open(local_path, encoding="utf-8").read()
    diff = "".join(difflib.unified_diff(
        _normalize(remote), _normalize(local),
        fromfile=source, tofile=local_path,
    ))

    if not diff:
        print(f"OK: {local_path} is in sync with {source}", flush=True)
        blocks = [
            _section(_title(":white_check_mark:")),
            _section("No changes"),
            {"type": "divider"},
            compared,
        ]
        return 0, _message(f"{PRODUCT}: no changes", blocks, COLOR_PASS)

    os.makedirs(os.path.dirname(DIFF_ARTIFACT), exist_ok=True)
    with open(DIFF_ARTIFACT, "w", encoding="utf-8") as f:
        f.write(diff)
    print(diff, end="", flush=True)

    blocks = [
        _section(_title(":rotating_light:")),
        _section("Changes detected"),
        {"type": "divider"},
        compared,
        *_diff_blocks(diff),
    ]
    return 1, _message(f"{PRODUCT}: changes detected", blocks, COLOR_FAIL)


def _post(channel: str, message: dict, token: str) -> None:
    body = json.dumps({"channel": channel, **message}).encode()
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
                   help="print the Block Kit payload instead of posting")
    a = p.parse_args(argv)

    token = os.environ.get("SLACK_OAUTH_TOKEN", "").strip()
    if not a.print_only:
        if not token:
            sys.exit("error: SLACK_OAUTH_TOKEN is not set")
        if not a.channel:
            sys.exit("error: no Slack channel (--channel or SLACK_CHANNEL_ID)")

    try:
        rc, message = _check(a.source_repo, a.source_ref, a.source_path, a.local_path)
    except Exception as exc:
        blocks = [
            _section(_title(":x:")),
            _section(f"proto diff check failed to run: `{exc}`"),
        ]
        crash = _message(f"{PRODUCT}: failed to run", blocks, COLOR_FAIL)
        print(json.dumps(crash, indent=2)) if a.print_only \
            else _post(a.channel, crash, token)
        raise

    if a.print_only:
        print(json.dumps(message, indent=2))
    else:
        _post(a.channel, message, token)
    return rc


if __name__ == "__main__":
    sys.exit(main())
