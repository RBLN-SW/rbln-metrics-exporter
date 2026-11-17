#!/usr/bin/env python3
"""Classify a whole PR and run its existing Piper bundle only when required."""

import argparse
import json
import os
from pathlib import Path, PurePosixPath
import re
import signal
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

CONFIG = Path(__file__).with_name("pr-ci.json")
DOC_SUFFIXES = {".md", ".rst", ".txt", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".pdf"}
ROOT_DOC = re.compile(r"(?:README(?:[.-][A-Za-z0-9_-]+)*|CHANGELOG|CONTRIBUTING|CODE_OF_CONDUCT|SECURITY)\.(?:md|rst|txt)\Z", re.I)
TERMINAL = {"passed", "failed", "broken", "canceled", "cancelled", "skipped", "not_run", "timed_out", "expired"}
RETRYABLE = {429, 500, 502, 503, 504}
RUN_LIMIT = 100
PIPER_STEP = "Validate or reuse Piper build for this PR"


class APIError(RuntimeError):
    def __init__(self, method, path, status=None):
        self.status = status
        super().__init__(f"{method} {path} failed (HTTP {status})" if status else
                         f"{method} {path} failed (transport error)")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Client:
    def __init__(self, origin, token):
        parsed = urllib.parse.urlsplit(origin)
        if (parsed.scheme != "https" or not parsed.netloc or parsed.username or
                parsed.password or parsed.path not in ("", "/") or parsed.query or parsed.fragment):
            raise ValueError("API URL must be an HTTPS origin")
        if not token.strip():
            raise ValueError("The API token is missing")
        self.origin, self.token = origin.rstrip("/"), token
        self.opener = urllib.request.build_opener(NoRedirect())

    def request(self, method, path, payload=None):
        if not path.startswith("/") or path.startswith("//"):
            raise ValueError("API path must be relative to the configured origin")
        request = urllib.request.Request(
            self.origin + path, method=method,
            data=None if payload is None else json.dumps(payload).encode(),
            headers={"Authorization": "Bearer " + self.token,
                     "Accept": "application/json", "Content-Type": "application/json"},
        )
        # Never retry a trigger POST: a lost response can still mean a live build.
        for attempt in range(3):
            try:
                with self.opener.open(request, timeout=30) as response:
                    body = response.read()
                    return json.loads(body) if body else {}
            except urllib.error.HTTPError as exc:
                error = APIError(method, path, exc.code)
            except (urllib.error.URLError, TimeoutError, OSError):
                error = APIError(method, path)
            if method != "GET" or attempt == 2 or error.status not in RETRYABLE | {None}:
                raise error from None
            time.sleep(2 ** attempt)


def is_document(path):
    if not isinstance(path, str) or not path or path.startswith("/"):
        return False
    parts = path.split("/")
    if any(part in ("", ".", "..") for part in parts):
        return False
    return bool(ROOT_DOC.fullmatch(path)) or (
        parts[0] == "docs" and len(parts) > 1 and PurePosixPath(path).suffix.lower() in DOC_SUFFIXES)


def docs_only(files):
    if not files:
        return False
    for file in files:
        if file.get("status") not in {"added", "removed", "modified", "renamed", "copied", "changed"}:
            return False
        if not is_document(file.get("filename")):
            return False
        if file.get("status") == "renamed" and not is_document(file.get("previous_filename")):
            return False
    return True


def pr_context(event, repository):
    pr = event["pull_request"]
    if event["repository"]["full_name"] != repository or pr["base"]["repo"]["full_name"] != repository:
        raise ValueError("PR repository mismatch")
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
        raise ValueError("Invalid repository")
    if not isinstance(pr["number"], int) or pr["number"] <= 0:
        raise ValueError("Invalid PR number")
    for side in ("base", "head"):
        if not re.fullmatch(r"[0-9a-f]{40}", pr[side]["sha"]):
            raise ValueError("PR requires full commit SHAs")
    branch = pr["base"]["ref"]
    if branch != "main":
        raise ValueError("The PR CI gate only handles PRs targeting main")
    return pr


def current_pr(github, repository, expected):
    current = github.request("GET", f"/repos/{repository}/pulls/{expected['number']}")
    if current.get("state") != "open":
        raise ValueError("PR is no longer open")
    for side in ("head", "base"):
        # The base branch can advance without a new PR event. Pin the head and
        # target branch; classify() separately checks base stability while paging.
        for field in (("sha", "ref") if side == "head" else ("ref",)):
            if current[side][field] != expected[side][field]:
                raise ValueError("PR changed during validation; use the latest workflow run")
        if current[side]["repo"]["full_name"] != expected[side]["repo"]["full_name"]:
            raise ValueError("PR repository changed during validation")
    return current


def classify(github, repository, pr):
    current = current_pr(github, repository, pr)
    count = current.get("changed_files")
    if not isinstance(count, int) or count < 0:
        raise ValueError("GitHub did not return a valid changed-file count")
    # GitHub's PR files endpoint is capped at 3000. Never classify a partial list as docs-only.
    if count == 0 or count > 3000:
        return True
    files = []
    for page in range(1, (count + 99) // 100 + 1):
        batch = github.request("GET", f"/repos/{repository}/pulls/{pr['number']}/files?per_page=100&page={page}")
        if not isinstance(batch, list):
            raise ValueError("Invalid PR file response")
        files.extend(batch)
    after = current_pr(github, repository, pr)
    if (after["base"]["sha"] != current["base"]["sha"] or
            after.get("changed_files") != count or len(files) != count or
            len({file["filename"] for file in files}) != count):
        raise ValueError("PR file list is incomplete or changed while being read")
    return not docs_only(files)


def trigger_payload(config, pr, env):
    if pr["base"]["ref"] != "main":
        raise ValueError("The PR CI gate only triggers main PR validation")
    if pr["head"]["repo"]["full_name"] != config["repository"]:
        raise ValueError("Fork code PRs cannot run the privileged internal CI; use an internal review branch")
    if pr.get("draft"):
        raise ValueError("Mark this code PR ready for review before running internal CI")
    return {
        "branch": pr["head"]["ref"], "commit": pr["head"]["sha"],
        "message": f"PR #{pr['number']} validation ({pr['head']['sha'][:12]})",
        "ignore_pipeline_branch_filters": True,
        "meta_data": {"github_pr": str(pr["number"]), "github_run_id": env.get("GITHUB_RUN_ID", ""),
                      "github_run_attempt": env.get("GITHUB_RUN_ATTEMPT", ""),
                      "github_pr_ci_version": "2",
                      "github_base_sha": pr["base"]["sha"],
                      "github_base_branch": pr["base"]["ref"],
                      "github_pr_repository": "https://github.com/" + config["repository"]},
        # Keep the existing context, and report the whole bundle even when a
        # component previously only reported a member-level status (e.g. NFD).
        "overrides": {"commit_status": {"enabled": True, "context": config["status_context"]}},
    }


def prepare(piper, config, payload):
    if not re.fullmatch(r"[A-Za-z0-9_-]+", config["bundle_id"]):
        raise ValueError("Invalid Piper bundle ID")
    root = "/api/bundles/" + config["bundle_id"]
    bundle = piper.request("GET", root)
    if bundle.get("ob_pipeline") != config["ob_pipeline"]:
        raise ValueError("Piper bundle pipeline mismatch")
    repo = piper.request("GET", "/api/repositories/" + bundle["repository_id"])
    if repo["owner"] + "/" + repo["name"] != config["repository"]:
        raise ValueError("Piper bundle repository mismatch")
    preview = piper.request("POST", root + "/preview", payload["overrides"])
    if not isinstance(preview.get("yaml"), str) or not preview["yaml"].strip():
        raise ValueError("Piper did not render a pipeline")
    return root


def build_identity(build, commit, number=None):
    actual = build.get("number")
    if not isinstance(actual, int) or actual <= 0 or (number is not None and actual != number):
        raise ValueError("Piper returned a different or invalid build number")
    if build.get("commit") != commit:
        raise ValueError("Piper build does not match the PR head commit")
    return actual


def list_runs(piper, config):
    runs = piper.request("GET", f"/api/bundles/{config['bundle_id']}/runs?limit={RUN_LIMIT}")
    if not isinstance(runs, list):
        raise ValueError("Invalid Piper run list")
    return runs


def matches_run(run, config, pr, workflow_run_id):
    trigger = run.get("trigger") or {}
    meta = trigger.get("meta_data") or {}
    return (run.get("bundle_id") == config["bundle_id"] and
            run.get("ob_pipeline") == config["ob_pipeline"] and
            run.get("repository") == config["repository"] and
            trigger.get("commit") == pr["head"]["sha"] and
            trigger.get("branch") == pr["head"]["ref"] and
            meta.get("github_run_id") == str(workflow_run_id) and
            meta.get("github_pr") == str(pr["number"]) and
            meta.get("github_base_branch") == pr["base"]["ref"] and
            meta.get("github_pr_repository") == "https://github.com/" + config["repository"])


def existing_build(piper, config, pr, env):
    """Reuse only this workflow's exact PR/bundle/commit, including Piper retries."""
    run_id = env.get("GITHUB_RUN_ID", "")
    if not re.fullmatch(r"[1-9][0-9]*", run_id):
        raise ValueError("GITHUB_RUN_ID is required to identify the PR validation")
    runs = list_runs(piper, config)
    matches = [run for run in runs if matches_run(run, config, pr, run_id)]
    if not matches:
        if len(runs) >= RUN_LIMIT and int(env.get("GITHUB_RUN_ATTEMPT", "1")) > 1:
            raise ValueError("Piper history lookup is bounded; inspect older runs before creating a duplicate build")
        return None
    # Never select an older passing run instead of the latest matching run.
    run = max(matches, key=lambda item: item.get("created_at", ""))
    number = run.get("ob_build")
    if not isinstance(number, int) or number < 1:
        raise ValueError("An existing Piper run has no confirmed build; inspect Piper Runs before retrying")
    build = piper.request("GET", f"/api/bundles/{config['bundle_id']}/builds/{number}")
    build_identity(build, pr["head"]["sha"], number)
    if build.get("branch") != pr["head"]["ref"]:
        raise ValueError("Piper build branch does not match the PR")
    return build


def start_or_reuse(piper, config, pr, env, root, payload):
    build = existing_build(piper, config, pr, env)
    if build is not None:
        return build, False
    # One POST only; retrying an ambiguous response can create a second build.
    try:
        return piper.request("POST", root + "/trigger", payload), True
    except (APIError, ValueError) as exc:
        raise RuntimeError(f"{exc}. Trigger was not retried; inspect Piper Runs before rerunning.") from None


def sync_results(piper, github, config, event):
    """Wake a failed Actions job after its exact Piper build has recovered.

    This runs trusted default-branch code. Events only nominate a run; Piper and
    GitHub are queried again before rerunning the original PR workflow job.
    """
    callback_run = event.get("workflow_run")
    if callback_run is not None:
        if callback_run.get("event") != "pull_request" or callback_run.get("conclusion") != "failure":
            return False
        sha = callback_run.get("head_sha", "")
        workflow_run_id = str(callback_run.get("id", ""))
        number = None
    else:
        if event.get("state") != "success" or event.get("context") != config["status_context"]:
            return False
        prefix = "https://obedients.k8s.rebellions.in/orgs/main/pipelines/" + config["ob_pipeline"] + "/builds/"
        match = re.fullmatch(re.escape(prefix) + r"([1-9][0-9]*)(?:[?#].*)?", event.get("target_url") or "")
        if not match:
            return False
        number, sha, workflow_run_id = int(match[1]), event.get("sha", ""), None
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        return False
    candidates = []
    for run in list_runs(piper, config):
        trigger = run.get("trigger") or {}
        meta = trigger.get("meta_data") or {}
        if (run.get("bundle_id") == config["bundle_id"] and
                run.get("ob_pipeline") == config["ob_pipeline"] and
                run.get("repository") == config["repository"] and
                trigger.get("commit") == sha and meta.get("github_pr_ci_version") == "2" and
                (number is None or run.get("ob_build") == number) and
                (workflow_run_id is None or meta.get("github_run_id") == workflow_run_id)):
            candidates.append(run)
    if not candidates:
        return False
    selected = max(candidates, key=lambda item: item.get("created_at", ""))
    meta = selected["trigger"]["meta_data"]
    workflow_run_id, pr_number = meta.get("github_run_id", ""), meta.get("github_pr", "")
    if not re.fullmatch(r"[1-9][0-9]*", workflow_run_id) or not re.fullmatch(r"[1-9][0-9]*", pr_number):
        return False
    repo_root = "/repos/" + config["repository"]
    pr = github.request("GET", f"{repo_root}/pulls/{pr_number}")
    if (pr.get("state") != "open" or pr.get("draft") or pr["head"]["sha"] != sha or
            pr["base"]["ref"] != "main" or
            pr["head"]["repo"]["full_name"] != config["repository"] or
            pr["base"]["repo"]["full_name"] != config["repository"]):
        return False
    build = existing_build(piper, config, pr, {"GITHUB_RUN_ID": workflow_run_id, "GITHUB_RUN_ATTEMPT": "2"})
    if build is None or build.get("state") != "passed" or build["number"] != selected.get("ob_build"):
        return False
    workflow = github.request("GET", f"{repo_root}/actions/runs/{workflow_run_id}")
    if (workflow.get("event") != "pull_request" or workflow.get("head_sha") != sha or
            workflow.get("path") != config["workflow_file"] or workflow.get("status") != "completed" or
            workflow.get("conclusion") != "failure" or workflow.get("run_attempt", 1) >= 3):
        return False
    jobs, page = [], 1
    while True:
        result = github.request("GET", f"{repo_root}/actions/runs/{workflow_run_id}/jobs?filter=latest&per_page=100&page={page}")
        batch = result["jobs"]
        jobs.extend(batch)
        if len(jobs) >= result["total_count"]:
            break
        if not batch:
            raise ValueError("Incomplete Actions job list")
        page += 1
    failed = [job for job in jobs if job.get("name") == "Piper PR validation" and
              job.get("status") == "completed" and job.get("conclusion") == "failure" and
              any(step.get("name") == PIPER_STEP for step in job.get("steps", []))]
    if len(failed) != 1:
        return False
    # Check the PR once more before the write. No PR checkout or build trigger.
    current_pr(github, config["repository"], pr)
    github.request("POST", f"{repo_root}/actions/jobs/{failed[0]['id']}/rerun", {})
    print(f"Requested Actions recheck for PR #{pr_number}, Piper build #{build['number']}", flush=True)
    return True


def wait_result(piper, github, root, repository, pr, number, timeout,
                clock=time.monotonic, sleep=time.sleep):
    deadline, previous = clock() + timeout, None
    while clock() < deadline:
        current_pr(github, repository, pr)
        build = piper.request("GET", f"{root}/builds/{number}")
        build_identity(build, pr["head"]["sha"], number)
        state = build.get("state")
        if state != previous:
            print(f"Piper build #{number}: {state}", flush=True)
            previous = state
        if state in TERMINAL:
            current_pr(github, repository, pr)
            if state != "passed":
                raise ValueError(f"Piper build #{number} ended with {state}")
            return
        sleep(min(30, max(0, deadline - clock())))
    raise TimeoutError(f"Piper build #{number} did not finish before the gate timeout")


def cancel_active(piper, config, root, number, commit):
    """Best-effort cleanup, restricted to the exact build this invocation created."""
    try:
        build = piper.request("GET", f"{root}/builds/{number}")
        build_identity(build, commit, number)
        if build.get("state") not in TERMINAL:
            piper.request("POST", f"/api/ob/pipelines/{config['ob_pipeline']}/builds/{number}/cancel", {})
            print(f"Requested cancellation of Piper build #{number}", flush=True)
    except (APIError, ValueError, KeyError, OSError) as exc:
        print(f"::warning::Build cleanup could not be confirmed: {exc}", file=sys.stderr)


def interrupted(signum, frame):
    raise InterruptedError("PR workflow was cancelled")


def emit(env, name, value):
    if env.get("GITHUB_OUTPUT"):
        with open(env["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
            output.write(f"{name}={value}\n")


def summary(env, message):
    print(message, flush=True)
    if env.get("GITHUB_STEP_SUMMARY"):
        with open(env["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as output:
            output.write(message + "\n")


def gate(needs):
    changes = needs["changes"]
    if changes["result"] != "success":
        raise ValueError("Change detection failed or was cancelled")
    required = changes.get("outputs", {}).get("needs_ci")
    result = needs["piper"]["result"]
    if required == "false" and result == "skipped":
        return "Documentation-only PR: no Buildkite build was created."
    if required == "true" and result == "success":
        return "The PR head commit passed its Piper CI bundle."
    raise ValueError("Required Piper validation did not pass")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["changes", "run", "gate", "sync"])
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    env = os.environ
    try:
        if args.command == "gate":
            summary(env, gate(json.loads(env["NEEDS_JSON"])))
            return 0
        if args.command == "changes":
            if env.get("GITHUB_EVENT_NAME") != "pull_request":
                emit(env, "needs_ci", "true")
                return 0
            event = json.loads(Path(env["GITHUB_EVENT_PATH"]).read_text())
            if event["pull_request"]["base"]["ref"] != "main":
                emit(env, "needs_ci", "true")
                return 0
        config = json.loads(CONFIG.read_text())
        event = json.loads(Path(env["GITHUB_EVENT_PATH"]).read_text())
        if env["GITHUB_REPOSITORY"] != config["repository"]:
            raise ValueError("Workflow repository does not match pr-ci.json")
        github = Client(env.get("GITHUB_API_URL", "https://api.github.com"), env.get("GITHUB_TOKEN", ""))
        if args.command == "sync":
            piper = Client(env.get("PIPER_URL", "https://piper.k8s.rebellions.in"), env.get("PIPER_TOKEN", ""))
            if args.dry_run:
                raise ValueError("Use unit tests for the sync callback; it has no dry-run mode")
            sync_results(piper, github, config, event)
            return 0
        pr = pr_context(event, config["repository"])
        required = classify(github, config["repository"], pr)
        if args.command == "changes":
            emit(env, "needs_ci", str(required).lower())
            summary(env, "Code/build/configuration validation is required." if required else
                    "Documentation-only PR: build, image scan and hardware validation are not required.")
            return 0
        if not required:
            raise ValueError("Documentation-only PR must not enter the Piper job")
        payload = trigger_payload(config, current_pr(github, config["repository"], pr), env)
        piper = Client(env.get("PIPER_URL", "https://piper.k8s.rebellions.in"), env.get("PIPER_TOKEN", ""))
        root = prepare(piper, config, payload)
        if args.dry_run:
            build = existing_build(piper, config, pr, env)
            print(json.dumps({"request": payload, "reuse_build": build["number"] if build else None}, indent=2))
            return 0
        current_pr(github, config["repository"], pr)
        build, created = start_or_reuse(piper, config, pr, env, root, payload)
        number = build_identity(build, pr["head"]["sha"])
        url = build.get("web_url") or build.get("url", "")
        emit(env, "build_number", number)
        summary(env, f"{'Created' if created else 'Reusing'} Piper build #{number}: {url}")
        for sig in (signal.SIGINT, signal.SIGTERM):
            signal.signal(sig, interrupted)
        try:
            wait_result(piper, github, root, config["repository"], pr, number, config["timeout_seconds"])
        except (Exception, KeyboardInterrupt):
            if created:
                cancel_active(piper, config, root, number, pr["head"]["sha"])
            raise
        return 0
    except (RuntimeError, ValueError, KeyError, TypeError, OSError, TimeoutError) as exc:
        print(f"::error::{exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
