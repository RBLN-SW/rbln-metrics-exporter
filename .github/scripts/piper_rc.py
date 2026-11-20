#!/usr/bin/env python3
"""Trigger an piper RC run and wait for its existing OB/GitHub verdict.

No image build, release publication, custom test verdict, or saved piper mutation.
Configuration beside this script identifies the repository and registered bundle.
"""
import argparse
import copy
import json
import os
from pathlib import Path
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

import yaml

CONFIG = Path(__file__).with_name("piper-rc.json")
PIPELINE_FIELDS = (
    "key", "name", "description", "cluster_id", "pool_id", "cluster_grace_minutes",
    "keep_cluster", "destroy_cluster", "sentinel_ref", "repo_ref", "enabled",
    "scenarios", "layout", "timeout_minutes", "env", "secrets", "chart",
    "runner", "commit_status",
)
OPERAND_PREFIXES = (
    "DEVICE_PLUGIN", "DRA_KUBELET_PLUGIN", "METRICS_EXPORTER",
    "NPU_FEATURE_DISCOVERY", "DRIVER_MANAGER", "CONTAINER_TOOLKIT",
    "SANDBOX_DEVICE_PLUGIN", "VFIO_MANAGER", "VFIO_MANAGER_DRIVER_MANAGER",
)
FINAL_STATES = {
    "passed": "success", "failed": "failure", "broken": "failure",
    "canceled": "error", "cancelled": "error", "skipped": "error",
    "not_run": "error", "timed_out": "error",
}


class APIError(RuntimeError):
    def __init__(self, method, path, status=None, service="piper"):
        self.status = status
        super().__init__(f"{service} {method} {path} failed" +
                         (f" (HTTP {status})" if status else " (transport error)"))


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None  # Never forward the bearer credential to another endpoint.


class Client:
    def __init__(self, url, token, service="piper"):
        self.service = service
        parsed = urllib.parse.urlsplit(url)
        if parsed.scheme != "https" or not parsed.netloc or parsed.username is not None or parsed.password is not None or parsed.query or parsed.fragment:
            raise ValueError(f"{service} URL must be an HTTPS origin")
        if parsed.path not in ("", "/"):
            raise ValueError(f"{service} URL must not contain an API path")
        if not token.strip():
            raise ValueError(f"{service} token is required")
        self.url, self.token = url.rstrip("/"), token
        self.opener = urllib.request.build_opener(NoRedirect())

    def request(self, method, path, payload=None):
        data = None if payload is None else json.dumps(payload).encode()
        request = urllib.request.Request(
            self.url + path, data=data, method=method,
            headers={"Authorization": "Bearer " + self.token,
                     "Accept": "application/json", "Content-Type": "application/json"},
        )
        try:
            with self.opener.open(request, timeout=60) as response:
                return json.load(response)
        except urllib.error.HTTPError as exc:
            raise APIError(method, path, exc.code, self.service) from None
        except (urllib.error.URLError, TimeoutError, OSError):
            raise APIError(method, path, service=self.service) from None


def required(env, key):
    value = env.get(key, "").strip()
    if not value:
        raise ValueError(f"{key} is required")
    return value


def image_env(prefix, registry, repository, tag):
    return {
        f"SENTINEL_{prefix}_IMAGE_REGISTRY": registry,
        f"SENTINEL_{prefix}_IMAGE_REPOSITORY": repository,
        f"SENTINEL_{prefix}_IMAGE_TAG": tag,
        f"SENTINEL_{prefix}_IMAGE_PULL_POLICY": "Always",
    }


def request_payload(config, env, pipelines):
    tag, sha = required(env, "RC_TAG"), required(env, "RC_SHA").lower()
    if not re.fullmatch(r"v\d+\.\d+\.\d+-rc\d+", tag):
        raise ValueError("RC_TAG must be vX.Y.Z-rcN")
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("RC_SHA must be the full release-tag commit SHA")
    if required(env, "GITHUB_REPOSITORY").lower() != config["repository"].lower():
        raise ValueError("The workflow repository does not match piper-rc.json")
    registry = required(env, "HARBOR_REGISTRY").rstrip("/")
    host, slash, project = registry.partition("/")
    if not slash or not project or "://" in registry or any(c.isspace() for c in registry):
        raise ValueError("HARBOR_REGISTRY must be registry-host/project")
    common = {}
    for image in config.get("images", []):
        common.update(image_env(image["prefix"], host, project + "/" + image["name"], tag))
    matrix = config.get("matrix", False)
    chart = None
    if matrix:
        # RC chart values select Operator, Validator and released operand images.
        # Kernel-driver/SMD/compiler pins remain hardware-specific piper settings.
        common.update({f"SENTINEL_{p}_{field}": ""
                       for p in ("OPERATOR", "VALIDATOR")
                       for field in ("REGISTRY", "REPOSITORY", "VERSION", "PULL_POLICY")})
        for prefix in OPERAND_PREFIXES:
            common.update({key: "" for key in image_env(prefix, "", "", "")})
        common.update({
            "SENTINEL_IMAGE_REGISTRY": "", "SENTINEL_IMAGE_TAG": "",
            "SENTINEL_IMAGE_PULL_POLICY": "",
            "SENTINEL_CATALOG_IMAGE": required(env, "SENTINEL_CATALOG_IMAGE"),
            "SENTINEL_HELM_CHART": f"oci://{registry}/rbln-npu-operator-chart",
            "SENTINEL_HELM_CHART_VERSION": tag.removeprefix("v"),
        })
        chart = {"source": "oci", "oci": common["SENTINEL_HELM_CHART"],
                 "version": common["SENTINEL_HELM_CHART_VERSION"]}
    elif env.get("OPERATOR_REF", "").strip():
        chart = {"source": "clone",
                 "repo": "https://github.com/RBLN-SW/rbln-npu-operator.git",
                 "ref": env["OPERATOR_REF"].strip(), "path": "deployments/rbln-npu-operator"}

    overrides = {
        "build": {"enabled": False}, "force_env": True, "env": common,
        "commit_status": {"enabled": True, "context": "rc-validation"},
        "status_target": {"repository": config["repository"], "sha": sha},
    }
    members = []
    for pipeline in pipelines:
        if chart is not None or env.get("SENTINEL_REF", "").strip():
            # Member input is a replacement, so preserve every unrelated setting.
            settings = {k: copy.deepcopy(pipeline[k]) for k in PIPELINE_FIELDS if k in pipeline}
            if chart is not None:
                settings["chart"] = copy.deepcopy(chart)
            if env.get("SENTINEL_REF", "").strip():
                settings["sentinel_ref"] = env["SENTINEL_REF"].strip()
            members.append({"id": pipeline["id"], "pipeline": settings})
    if members:
        overrides["members"] = members
    return {
        "branch": env.get("PIPER_VALIDATE_BRANCH", "").strip() or config["execution_branch"]
                  if matrix else required(env, "RC_BRANCH"),
        "commit": env.get("PIPER_VALIDATE_COMMIT", "").strip() or "HEAD" if matrix else sha,
        "message": f"rc-validation {tag}",
        "meta_data": {"rc_tag": tag, "rc_repository": config["repository"], "rc_sha": sha},
        "overrides": overrides,
    }


def prepare(client, config, env):
    bundle_id = env.get("PIPER_RC_BUNDLE_ID", "").strip() or config["bundle_id"]
    if not re.fullmatch(r"[A-Za-z0-9_-]+", bundle_id):
        raise ValueError("Invalid PIPER_RC_BUNDLE_ID")
    root = f"/api/bundles/{bundle_id}"
    bundle = client.request("GET", root)
    if bundle.get("ob_pipeline") != config["ob_pipeline"]:
        raise ValueError("piper bundle is not linked to the expected OB pipeline")
    repo = client.request("GET", "/api/repositories/" + bundle["repository_id"])
    if (repo["owner"] + "/" + repo["name"]).lower() != config["execution_repository"].lower():
        raise ValueError("piper bundle belongs to a different execution repository")
    pipelines = [client.request("GET", "/api/pipelines/" + member["id"])
                 for member in bundle["pipelines"]]
    payload = request_payload(config, env, pipelines)
    return root, bundle, payload


def matches_inline_bootstrap(content, configuration):
    """Compare OB's single upload step with the bootstrap generated for this bundle."""
    if not isinstance(content, str) or not isinstance(configuration, str):
        return False
    try:
        expected = yaml.safe_load(content)
        actual = yaml.safe_load(configuration)
    except yaml.YAMLError:
        return False

    for document in (expected, actual):
        if not isinstance(document, dict):
            return False
        steps = document.get("steps")
        if not isinstance(steps, list) or len(steps) != 1:
            return False
        step = steps[0]
        if not isinstance(step, dict) or not isinstance(step.get("command"), str):
            return False
        step["command"] = step["command"].rstrip("\n")
        if not step["command"].strip():
            return False
        step.pop("label", None)
        # OB may limit checkout to the bootstrap directory before running the same command.
        if step.get("plugins") == [{"sparse-checkout#v1.6.0": {"paths": [".buildkite"]}}]:
            del step["plugins"]
    return actual == expected


def ensure_bootstrap(client, root, ob_pipeline):
    bootstrap = client.request("GET", root + "/bootstrap")
    if not bootstrap.get("matches"):
        raise ValueError("Merge the piper bootstrap into the execution branch and sync the piper repository")
    ob = client.request("GET", root + "/ob/bootstrap")
    if ob.get("matches"):
        return
    pipeline = client.request("GET", "/api/ob/pipelines/" + urllib.parse.quote(ob_pipeline, safe=""))
    if not matches_inline_bootstrap(bootstrap.get("content"), pipeline.get("configuration")):
        raise ValueError("The OB entry point must upload the registered piper bootstrap file "
                         "or run its bundle upload command inline with PIPER_RUN")


def cancel_superseded(client, root, config, payload):
    """Preserve the Operator's optional best-effort cancellation of older RCs."""
    if not config.get("matrix"):
        return
    tag = payload["meta_data"]["rc_tag"]
    version, number = tag.rsplit("-rc", 1)
    try:
        builds = client.request("GET", root + "/builds?limit=50")
        for build in builds:
            match = re.fullmatch(r"rc-validation " + re.escape(version) + r"-rc(\d+)",
                                 build.get("message", ""))
            if (not match or int(match.group(1)) >= int(number)
                    or build.get("branch") != payload["branch"]
                    or build.get("state") not in ("scheduled", "running", "creating", "blocked")):
                continue
            build_number = build.get("number")
            if not isinstance(build_number, int) or build_number < 1:
                continue
            try:
                client.request("POST", f"/api/ob/pipelines/{config['ob_pipeline']}/builds/{build_number}/cancel")
                print(f"Requested cancellation of superseded RC build #{build_number}", flush=True)
            except (APIError, ValueError):
                print(f"::warning::Could not cancel superseded RC build #{build_number}; continuing")
    except (APIError, ValueError):
        print("::warning::Could not inspect superseded RC runs; continuing")


def github_build_status(github, target, build_url):
    path = f"/repos/{target['repository']}/commits/{target['sha']}/statuses"
    page = 1
    while True:
        statuses = github.request("GET", f"{path}?per_page=100&page={page}")
        for status in statuses:  # GitHub returns the latest update first.
            if status["context"] == "rc-validation" and status.get("target_url") == build_url:
                return status["state"]
        if len(statuses) < 100:
            return None
        page += 1


def delivered_status(run, target, build_url, github):
    delivery = run.get("status_delivery")
    if not delivery:
        # Native OB notifications go directly to GitHub.
        if github:
            return github_build_status(github, target, build_url)
        return None
    actual = delivery.get("target") or {}
    if (delivery.get("error") or actual.get("repository", "").lower() != target["repository"].lower()
            or actual.get("sha", "").lower() != target["sha"]):
        return None
    sent = delivery.get("sent") or {}
    return sent.get("rc-validation") or sent.get(
        target["repository"].lower() + "@" + target["sha"] + ":rc-validation")


def wait_result(client, root, build, payload, timeout, poll=15, clock=time.monotonic, sleep=time.sleep,
                github=None):
    deadline = clock() + timeout
    number, run_id = build["number"], build["env"]["PIPER_RUN"]
    target = payload["overrides"]["status_target"]
    previous = None
    while clock() < deadline:
        try:
            current = client.request("GET", f"{root}/builds/{number}")
            run = client.request("GET", "/api/runs/" + run_id)
            if current.get("number") != number or run.get("id") != run_id or run.get("ob_build") != number:
                raise ValueError("piper returned a different build/run")
            state = current.get("state", "unknown")
            if state != previous:
                print(f"piper build #{number}: {state}", flush=True)
                previous = state
            verdict = FINAL_STATES.get(state)
            if verdict and delivered_status(run, target, build["url"], github) == verdict:
                return state, verdict
        except APIError as exc:
            if exc.status not in (None, 429, 500, 502, 503, 504):
                raise
        sleep(min(poll, max(0, deadline - clock())))
    raise TimeoutError(f"Timed out waiting for piper build #{number} and its rc-validation delivery: {build['url']}")


def run(client, config, env, dry_run=False):
    root, bundle, payload = prepare(client, config, env)
    # Preview does not create a run, dispatch jobs or publish commit statuses.
    preview = client.request("POST", root + "/preview", payload["overrides"])
    if "yaml" not in preview or "PIPER_COMMIT_TAG_ENV:" in preview["yaml"]:
        raise ValueError("piper must support per-run build disabling (version 0.73.0 or newer)")
    if dry_run:
        print(json.dumps(payload, indent=2))
        return 0
    ensure_bootstrap(client, root, bundle["ob_pipeline"])
    github = None
    if env.get("GITHUB_TOKEN"):
        github = Client("https://api.github.com", env["GITHUB_TOKEN"], "GitHub")
    cancel_superseded(client, root, config, payload)
    # One POST only. A timeout may have created the build; never replay it.
    try:
        build = client.request("POST", root + "/trigger", payload)
    except (APIError, ValueError) as exc:
        raise RuntimeError(f"{exc}. Trigger was not retried; inspect piper Runs before retrying.") from None
    run_id = build.get("env", {}).get("PIPER_RUN", "")
    number = build.get("number")
    url = build.get("url", "")
    if not re.fullmatch(r"[A-Za-z0-9_-]+", run_id) or not isinstance(number, int) or number < 1:
        raise ValueError("Trigger returned no valid run/build identity; inspect piper Runs before retrying")
    if not url.startswith("https://") or "\n" in url or "\r" in url:
        raise ValueError("Trigger returned no valid build URL; inspect piper Runs before retrying")
    print(f"RC validation: {url} (piper run {run_id})", flush=True)
    if env.get("GITHUB_OUTPUT"):
        with open(env["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
            output.write(f"build_url={url}\nrun_id={run_id}\nbuild_number={number}\n")
    if env.get("GITHUB_STEP_SUMMARY"):
        with open(env["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as summary:
            summary.write(f"RC validation for {env['RC_TAG']}: {url}\n\npiper run: {run_id}\n")
    state, verdict = wait_result(client, root, build, payload, config["timeout_seconds"], github=github)
    print(f"piper/OB result: {state}; rc-validation delivery: {verdict}", flush=True)
    return 0 if verdict == "success" else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dry-run", action="store_true", help="Read configuration and preview only; never trigger")
    args = parser.parse_args()
    try:
        config = json.loads(CONFIG.read_text())
        client = Client(os.environ.get("PIPER_URL", "https://piper.k8s.rebellions.in"),
                        os.environ.get("PIPER_TOKEN", ""))
        return run(client, config, os.environ, args.dry_run)
    except (APIError, ValueError, RuntimeError, TimeoutError, KeyError) as exc:
        print(f"::error::{exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
