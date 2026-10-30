#!/usr/bin/env python3

# Copyright 2026 Rebellions Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Trigger an RNPM RC run and wait for its existing OB/GitHub verdict.

No image build, release publication, custom test verdict, or saved RNPM mutation.
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

CONFIG = Path(__file__).with_name("rnpm-rc.json")
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
    def __init__(self, method, path, status=None):
        self.status = status
        super().__init__(f"RNPM {method} {path} failed" +
                         (f" (HTTP {status})" if status else " (transport error)"))


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None  # Never forward the bearer credential to another endpoint.


class Client:
    def __init__(self, url, token):
        parsed = urllib.parse.urlsplit(url)
        if parsed.scheme != "https" or not parsed.netloc or parsed.username is not None or parsed.password is not None or parsed.query or parsed.fragment:
            raise ValueError("RNPM_URL must be an HTTPS origin")
        if parsed.path not in ("", "/"):
            raise ValueError("RNPM_URL must not contain an API path")
        if not token.strip():
            raise ValueError("GitHub Actions secret RNPM_TOKEN is required")
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
            raise APIError(method, path, exc.code) from None
        except (urllib.error.URLError, TimeoutError, OSError):
            raise APIError(method, path) from None


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
        raise ValueError("The workflow repository does not match rnpm-rc.json")
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
        # Kernel-driver/SMD/compiler pins remain hardware-specific RNPM settings.
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
        "branch": env.get("RNPM_VALIDATE_BRANCH", "").strip() or config["execution_branch"]
                  if matrix else required(env, "RC_BRANCH"),
        "commit": env.get("RNPM_VALIDATE_COMMIT", "").strip() or "HEAD" if matrix else sha,
        "message": f"rc-validation {tag}",
        "meta_data": {"rc_tag": tag, "rc_repository": config["repository"], "rc_sha": sha},
        "overrides": overrides,
    }


def prepare(client, config, env):
    bundle_id = env.get("RNPM_RC_BUNDLE_ID", "").strip() or config["bundle_id"]
    if not re.fullmatch(r"[A-Za-z0-9_-]+", bundle_id):
        raise ValueError("Invalid RNPM_RC_BUNDLE_ID")
    root = f"/api/bundles/{bundle_id}"
    bundle = client.request("GET", root)
    if bundle.get("ob_pipeline") != config["ob_pipeline"]:
        raise ValueError("RNPM bundle is not linked to the expected OB pipeline")
    repo = client.request("GET", "/api/repositories/" + bundle["repository_id"])
    if (repo["owner"] + "/" + repo["name"]).lower() != config["execution_repository"].lower():
        raise ValueError("RNPM bundle belongs to a different execution repository")
    pipelines = [client.request("GET", "/api/pipelines/" + member["id"])
                 for member in bundle["pipelines"]]
    payload = request_payload(config, env, pipelines)
    return root, bundle, payload


def ensure_bootstrap(client, root):
    bootstrap = client.request("GET", root + "/bootstrap")
    if not bootstrap.get("matches"):
        raise ValueError("Merge the RNPM bootstrap into the execution branch and sync the RNPM repository")
    ob = client.request("GET", root + "/ob/bootstrap")
    if not ob.get("matches"):
        raise ValueError("The OB entry point still uses the old pipeline; switch it to the RNPM bootstrap")


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


def wait_result(client, root, build, payload, timeout, poll=15, clock=time.monotonic, sleep=time.sleep):
    deadline = clock() + timeout
    number, run_id = build["number"], build["env"]["RNPM_RUN"]
    target = payload["overrides"]["status_target"]
    previous = None
    while clock() < deadline:
        try:
            current = client.request("GET", f"{root}/builds/{number}")
            run = client.request("GET", "/api/runs/" + run_id)
        except APIError as exc:
            if exc.status not in (None, 429, 500, 502, 503, 504):
                raise
            sleep(min(poll, max(0, deadline - clock())))
            continue
        if current.get("number") != number or run.get("id") != run_id or run.get("ob_build") != number:
            raise ValueError("RNPM returned a different build/run")
        delivery = run.get("status_delivery") or {}
        state = current.get("state", "unknown")
        if state != previous:
            print(f"RNPM build #{number}: {state}", flush=True)
            previous = state
        actual_target = delivery.get("target") or {}
        if (actual_target.get("repository", "").lower() == target["repository"].lower()
                and actual_target.get("sha", "").lower() == target["sha"]):
            sent = delivery.get("sent") or {}
            delivered = sent.get("rc-validation") or sent.get(
                target["repository"].lower() + "@" + target["sha"] + ":rc-validation")
            verdict = FINAL_STATES.get(state)
            if verdict and delivered == verdict and not delivery.get("error"):
                return state, verdict
        sleep(min(poll, max(0, deadline - clock())))
    raise TimeoutError(f"Timed out waiting for RNPM build #{number} and its rc-validation delivery: {build['url']}")


def run(client, config, env, dry_run=False):
    root, bundle, payload = prepare(client, config, env)
    # Preview does not create a run, dispatch jobs or publish commit statuses.
    preview = client.request("POST", root + "/preview", payload["overrides"])
    if "yaml" not in preview or "RNPM_COMMIT_TAG_ENV:" in preview["yaml"]:
        raise ValueError("RNPM must support per-run build disabling (version 0.73.0 or newer)")
    if dry_run:
        print(json.dumps(payload, indent=2))
        return 0
    ensure_bootstrap(client, root)
    cancel_superseded(client, root, config, payload)
    # One POST only. A timeout may have created the build; never replay it.
    try:
        build = client.request("POST", root + "/trigger", payload)
    except (APIError, ValueError) as exc:
        raise RuntimeError(f"{exc}. Trigger was not retried; inspect RNPM Runs before retrying.") from None
    run_id = build.get("env", {}).get("RNPM_RUN", "")
    number = build.get("number")
    url = build.get("url", "")
    if not re.fullmatch(r"[A-Za-z0-9_-]+", run_id) or not isinstance(number, int) or number < 1:
        raise ValueError("Trigger returned no valid run/build identity; inspect RNPM Runs before retrying")
    if not url.startswith("https://") or "\n" in url or "\r" in url:
        raise ValueError("Trigger returned no valid build URL; inspect RNPM Runs before retrying")
    print(f"RC validation: {url} (RNPM run {run_id})", flush=True)
    if env.get("GITHUB_OUTPUT"):
        with open(env["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
            output.write(f"build_url={url}\nrun_id={run_id}\nbuild_number={number}\n")
    if env.get("GITHUB_STEP_SUMMARY"):
        with open(env["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as summary:
            summary.write(f"RC validation for {env['RC_TAG']}: {url}\n\nRNPM run: {run_id}\n")
    state, verdict = wait_result(client, root, build, payload, config["timeout_seconds"])
    print(f"RNPM/OB result: {state}; rc-validation delivery: {verdict}", flush=True)
    return 0 if verdict == "success" else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dry-run", action="store_true", help="Read configuration and preview only; never trigger")
    args = parser.parse_args()
    try:
        config = json.loads(CONFIG.read_text())
        client = Client(os.environ.get("RNPM_URL", "https://rnpm.k8s.rebellions.in"),
                        os.environ.get("RNPM_TOKEN", ""))
        return run(client, config, os.environ, args.dry_run)
    except (APIError, ValueError, RuntimeError, TimeoutError, KeyError) as exc:
        print(f"::error::{exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
