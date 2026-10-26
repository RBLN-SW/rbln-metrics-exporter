"""Offline contract tests: all RNPM requests are intercepted."""
import copy
import io
import json
import unittest
from contextlib import redirect_stdout
from unittest.mock import patch
import rnpm_rc as rc

SHA = "a" * 40
ENV = {
    "GITHUB_REPOSITORY": "RBLN-SW/k8s-device-plugin",
    "RC_TAG": "v1.2.3-rc4", "RC_SHA": SHA, "RC_BRANCH": "release-1.2.3",
    "HARBOR_REGISTRY": "registry.example/team/nested",
}
CONFIG = {
    "repository": "RBLN-SW/k8s-device-plugin",
    "execution_repository": "RBLN-SW/k8s-device-plugin",
    "bundle_id": "bundle", "ob_pipeline": "pr-device", "timeout_seconds": 100,
    "images": [{"name": "k8s-device-plugin", "prefix": "DEVICE_PLUGIN"}],
}
PIPELINE = {
    "id": "pipeline", "repository_id": "repository", "key": "pr", "name": "pr",
    "cluster_id": "cluster", "pool_id": "", "cluster_grace_minutes": 0,
    "keep_cluster": False, "destroy_cluster": False, "enabled": True,
    "sentinel_ref": "dev", "repo_ref": "", "layout": "grouped", "timeout_minutes": 180,
    "env": {"SENTINEL_ATOM_DRIVER_VERSION": "driver-pin",
            "SENTINEL_OPERATOR_VERSION": "dev"},
    "secrets": {"SENTINEL_GIT_TOKEN": "GIT_PAT"},
    "chart": {"source": "clone", "repo": "saved-chart", "ref": "main", "path": "chart"},
    "runner": {"nuke": True, "teardown": "inline"},
    "commit_status": {"enabled": True, "context": "component-ci"},
    "scenarios": [{"family": "components/device_plugin", "subtests": []}],
}
BUILD = {"number": 42, "url": "https://ob.example/builds/42", "env": {"RNPM_RUN": "run42"}}


class Fake:
    def __init__(self):
        self.calls = []
        self.bootstrap = True
        self.entrypoint = True
        self.trigger_error = None

    def request(self, method, path, payload=None):
        self.calls.append((method, path, payload))
        if path == "/api/bundles/bundle":
            return {"repository_id": "repository", "ob_pipeline": "pr-device",
                    "pipelines": [{"id": "pipeline"}]}
        if path == "/api/repositories/repository":
            return {"owner": "RBLN-SW", "name": "k8s-device-plugin"}
        if path == "/api/pipelines/pipeline":
            return copy.deepcopy(PIPELINE)
        if path.endswith("/preview"):
            return {"yaml": "steps: []", "warnings": []}
        if path.endswith("/ob/bootstrap"):
            return {"matches": self.entrypoint}
        if path.endswith("/bootstrap"):
            return {"matches": self.bootstrap}
        if path.endswith("/trigger"):
            if self.trigger_error:
                raise self.trigger_error
            return copy.deepcopy(BUILD)
        raise AssertionError((method, path))


class Clock:
    def __init__(self):
        self.now = 0
    def time(self):
        return self.now
    def sleep(self, duration):
        self.now += duration


class PayloadTests(unittest.TestCase):
    def test_component_reuses_published_image_without_changing_saved_settings(self):
        before = copy.deepcopy(PIPELINE)
        p = rc.request_payload(CONFIG, ENV, [PIPELINE])
        self.assertEqual(p["branch"], "release-1.2.3")
        self.assertEqual(p["commit"], SHA)
        o = p["overrides"]
        self.assertEqual(o["build"], {"enabled": False})
        self.assertTrue(o["force_env"])
        self.assertEqual(o["env"]["SENTINEL_DEVICE_PLUGIN_IMAGE_REGISTRY"], "registry.example")
        self.assertEqual(o["env"]["SENTINEL_DEVICE_PLUGIN_IMAGE_REPOSITORY"], "team/nested/k8s-device-plugin")
        self.assertEqual(o["env"]["SENTINEL_DEVICE_PLUGIN_IMAGE_TAG"], "v1.2.3-rc4")
        self.assertEqual(o["commit_status"], {"enabled": True, "context": "rc-validation"})
        self.assertEqual(o["status_target"]["sha"], SHA)
        self.assertNotIn("members", o)
        self.assertNotIn("RELEASE_MODE", o["env"])
        self.assertEqual(PIPELINE, before)

    def test_all_component_image_contracts(self):
        cases = [
            ("k8s-device-plugin", "DEVICE_PLUGIN"),
            ("rbln-metrics-exporter", "METRICS_EXPORTER"),
            ("rbln-npu-feature-discovery", "NPU_FEATURE_DISCOVERY"),
            ("k8s-dra-driver-npu", "DRA_KUBELET_PLUGIN"),
            ("sandbox-device-plugin", "SANDBOX_DEVICE_PLUGIN"),
        ]
        for name, prefix in cases:
            with self.subTest(name=name):
                cfg = dict(CONFIG, images=[{"name": name, "prefix": prefix}])
                e = rc.request_payload(cfg, ENV, [PIPELINE])["overrides"]["env"]
                self.assertEqual(e[f"SENTINEL_{prefix}_IMAGE_TAG"], ENV["RC_TAG"])
                self.assertEqual(e[f"SENTINEL_{prefix}_IMAGE_REPOSITORY"], "team/nested/" + name)

    def test_driver_manager_updates_both_operands(self):
        cfg = dict(CONFIG, images=[{"name": "rbln-k8s-driver-manager", "prefix": p}
                                  for p in ("DRIVER_MANAGER", "VFIO_MANAGER_DRIVER_MANAGER")])
        e = rc.request_payload(cfg, ENV, [PIPELINE])["overrides"]["env"]
        for prefix in ("DRIVER_MANAGER", "VFIO_MANAGER_DRIVER_MANAGER"):
            self.assertEqual(e[f"SENTINEL_{prefix}_IMAGE_TAG"], ENV["RC_TAG"])
            self.assertEqual(e[f"SENTINEL_{prefix}_IMAGE_REPOSITORY"], "team/nested/rbln-k8s-driver-manager")

    def test_operator_uses_rc_chart_and_keeps_hardware_pins(self):
        cfg = dict(CONFIG, matrix=True, images=[], execution_branch="dev",
                   repository="RBLN-SW/rbln-npu-operator",
                   execution_repository="rebellions-sw/npu-operator-test-infra")
        env = dict(ENV, GITHUB_REPOSITORY=cfg["repository"],
                   SENTINEL_CATALOG_IMAGE="registry.example/catalog@sha256:" + "b" * 64,
                   SENTINEL_REF="release-harness")
        p = rc.request_payload(cfg, env, [PIPELINE])
        self.assertEqual((p["branch"], p["commit"]), ("dev", "HEAD"))
        o = p["overrides"]
        self.assertEqual(o["status_target"], {"repository": cfg["repository"], "sha": SHA})
        self.assertEqual(o["env"]["SENTINEL_HELM_CHART_VERSION"], "1.2.3-rc4")
        for prefix in rc.OPERAND_PREFIXES:
            self.assertEqual(o["env"][f"SENTINEL_{prefix}_IMAGE_TAG"], "")
        self.assertEqual(o["env"]["SENTINEL_OPERATOR_VERSION"], "")
        self.assertEqual(o["env"]["SENTINEL_VALIDATOR_VERSION"], "")
        self.assertNotIn("SENTINEL_ATOM_DRIVER_VERSION", o["env"])
        member = o["members"][0]["pipeline"]
        self.assertEqual(member["chart"], {"source": "oci",
                         "oci": "oci://registry.example/team/nested/rbln-npu-operator-chart",
                         "version": "1.2.3-rc4"})
        self.assertEqual(member["sentinel_ref"], "release-harness")
        self.assertEqual(member["env"], PIPELINE["env"])
        self.assertEqual(member["secrets"], PIPELINE["secrets"])
        self.assertEqual(member["scenarios"], PIPELINE["scenarios"])
        self.assertEqual(member["timeout_minutes"], 180)
        self.assertNotIn("repository_id", member)

    def test_optional_refs_use_member_settings_not_reserved_env(self):
        env = dict(ENV, OPERATOR_REF="operator-sha", SENTINEL_REF="harness-tag")
        o = rc.request_payload(CONFIG, env, [PIPELINE])["overrides"]
        member = o["members"][0]["pipeline"]
        self.assertEqual(member["chart"]["ref"], "operator-sha")
        self.assertEqual(member["sentinel_ref"], "harness-tag")
        self.assertEqual(member["runner"], PIPELINE["runner"])
        self.assertNotIn("SENTINEL_REF", o["env"])

    def test_invalid_inputs_fail_before_trigger(self):
        for key, value in [("RC_TAG", "v1.2.3"), ("RC_SHA", "HEAD"),
                           ("HARBOR_REGISTRY", "https://registry.example/team"),
                           ("GITHUB_REPOSITORY", "other/repository")]:
            with self.subTest(key=key):
                api = Fake()
                with self.assertRaises(ValueError):
                    rc.run(api, CONFIG, dict(ENV, **{key: value}))
                self.assertFalse(any(path.endswith("/trigger") for _, path, _ in api.calls))

    def test_actual_config_has_expected_repository_mapping(self):
        config = json.loads(rc.CONFIG.read_text())
        self.assertIn(config["repository"].split("/")[1], {
            "k8s-device-plugin", "rbln-metrics-exporter", "rbln-k8s-driver-manager",
            "sandbox-device-plugin", "rbln-k8s-dra-driver", "rbln-npu-feature-discovery",
            "rbln-npu-operator"})
        self.assertTrue(config["bundle_id"])
        self.assertTrue(config["ob_pipeline"])
        self.assertEqual(config.get("matrix", False), config["repository"].endswith("/rbln-npu-operator"))


class ExecutionTests(unittest.TestCase):
    def test_dry_run_only_reads_and_previews(self):
        api = Fake()
        with redirect_stdout(io.StringIO()):
            self.assertEqual(rc.run(api, CONFIG, ENV, dry_run=True), 0)
        self.assertEqual([path for method, path, _ in api.calls if method == "POST"],
                         ["/api/bundles/bundle/preview"])

    def test_bootstrap_and_entrypoint_must_be_ready(self):
        for field in ("bootstrap", "entrypoint"):
            api = Fake()
            setattr(api, field, False)
            with self.assertRaises(ValueError):
                rc.run(api, CONFIG, ENV)
            self.assertFalse(any(path.endswith("/trigger") for _, path, _ in api.calls))

    def test_ambiguous_trigger_is_never_retried(self):
        api = Fake()
        api.trigger_error = rc.APIError("POST", "/api/bundles/bundle/trigger", 504)
        with self.assertRaisesRegex(RuntimeError, "not retried"):
            rc.run(api, CONFIG, ENV)
        self.assertEqual(sum(path.endswith("/trigger") for _, path, _ in api.calls), 1)

    def test_old_rnpm_build_override_is_rejected(self):
        api = Fake()
        original = api.request
        def request(method, path, payload=None):
            if path.endswith("/preview"):
                return {"yaml": "env:\n  RNPM_COMMIT_TAG_ENV: SENTINEL_DEVICE_PLUGIN_IMAGE_TAG\n"}
            return original(method, path, payload)
        api.request = request
        with self.assertRaises(ValueError):
            rc.run(api, CONFIG, ENV)

    def test_uses_existing_rnpm_ob_verdict_for_workflow_result(self):
        for state, verdict, expected in [("passed", "success", 0), ("failed", "failure", 1),
                                          ("canceled", "error", 1)]:
            with self.subTest(state=state), patch.object(rc, "wait_result", return_value=(state, verdict)):
                with redirect_stdout(io.StringIO()):
                    self.assertEqual(rc.run(Fake(), CONFIG, ENV), expected)

    def test_waits_for_status_delivery_after_build_passes(self):
        clock = Clock()
        class Poll:
            def request(self, method, path, payload=None):
                if "/builds/" in path:
                    return {"number": 42, "state": "passed"}
                return {"id": "run42", "ob_build": 42, "status_delivery": {
                    "target": {"repository": CONFIG["repository"], "sha": SHA},
                    "sent": {"rc-validation": "pending" if clock.now < 15 else "success"},
                    "error": ""}}
        payload = rc.request_payload(CONFIG, ENV, [PIPELINE])
        self.assertEqual(rc.wait_result(Poll(), "/api/bundles/bundle", BUILD, payload, 60,
                                       clock=clock.time, sleep=clock.sleep), ("passed", "success"))
        self.assertEqual(clock.now, 15)

    def test_wrong_sha_or_unsent_status_cannot_succeed(self):
        clock = Clock()
        class Poll:
            def request(self, method, path, payload=None):
                if "/builds/" in path:
                    return {"number": 42, "state": "passed"}
                return {"id": "run42", "ob_build": 42, "status_delivery": {
                    "target": {"repository": CONFIG["repository"], "sha": "b" * 40},
                    "sent": {"rc-validation": "success"}, "error": ""}}
        with self.assertRaises(TimeoutError):
            rc.wait_result(Poll(), "/api/bundles/bundle", BUILD,
                           rc.request_payload(CONFIG, ENV, [PIPELINE]), 30,
                           clock=clock.time, sleep=clock.sleep)

    def test_only_older_operator_rcs_are_canceled(self):
        class Builds:
            calls = []
            def request(self, method, path, payload=None):
                self.calls.append((method, path))
                if method == "POST":
                    return {}
                return [
                    {"number": 1, "message": "rc-validation v1.2.3-rc3", "branch": "dev", "state": "running"},
                    {"number": 2, "message": "rc-validation v1.2.3-rc4", "branch": "dev", "state": "running"},
                    {"number": 3, "message": "rc-validation v1.2.2-rc1", "branch": "dev", "state": "running"},
                    {"number": 4, "message": "rc-validation v1.2.3-rc2", "branch": "dev", "state": "passed"},
                    {"number": 5, "message": "manual investigation", "branch": "dev", "state": "running"},
                ]
        api = Builds()
        rc.cancel_superseded(api, "/api/bundles/bundle", dict(CONFIG, matrix=True),
                             {"branch": "dev", "meta_data": {"rc_tag": "v1.2.3-rc4"}})
        self.assertEqual([path for method, path in api.calls if method == "POST"],
                         ["/api/ob/pipelines/pr-device/builds/1/cancel"])

    def test_http_client_rejects_insecure_origins_and_missing_credentials(self):
        for url, token in [("http://rnpm.example", "secret"), ("https://rnpm.example/api", "secret"),
                           ("https://user@rnpm.example", "secret"), ("https://rnpm.example", "")]:
            with self.assertRaises(ValueError):
                rc.Client(url, token)


if __name__ == "__main__":
    unittest.main()
