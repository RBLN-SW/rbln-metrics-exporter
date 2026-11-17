"""Safety contracts for docs-only skips, PR identity and the required CI gate."""

import copy
import io
import json
import unittest
import urllib.error
from unittest.mock import patch

import pr_ci

REPO = "RBLN-SW/component"
SHA = "a" * 40
CONFIG = {"repository": REPO, "bundle_id": "bundle", "ob_pipeline": "pr-component",
          "status_context": "[Component] PR CI", "workflow_file": ".github/workflows/trigger-pr.yaml"}
PR = {"number": 42, "state": "open", "draft": False, "changed_files": 1,
      "head": {"sha": SHA, "ref": "feature", "repo": {"full_name": REPO}},
      "base": {"sha": "b" * 40, "ref": "main", "repo": {"full_name": REPO}}}


def file(path, status="modified", **extra):
    return {"filename": path, "status": status, **extra}


class FakeClient:
    def __init__(self, replies):
        self.replies = list(replies)
        self.calls = []

    def request(self, method, path, payload=None):
        self.calls.append((method, path, payload))
        reply = self.replies.pop(0)
        if isinstance(reply, Exception):
            raise reply
        return copy.deepcopy(reply)


class ClassificationTests(unittest.TestCase):
    def test_prose_and_document_assets(self):
        self.assertTrue(pr_ci.docs_only([file(p) for p in (
            "README.md", "README.ko.md", "CHANGELOG.md", "CONTRIBUTING.md",
            "docs/guide.md", "docs/images/architecture.svg", "docs/guide.rst")]))

    def test_build_inputs_and_executable_examples_are_not_docs(self):
        for path in ("main.go", "go.mod", "go.sum", "Dockerfile", "Makefile",
                     ".github/workflows/ci.yaml", ".buildkite/pipeline.yml",
                     "deployments/values.yaml", "docs/examples/demo.yaml",
                     "docs/test.py", "docs/Makefile", "fixtures/README.md",
                     "pkg/README.md", "LICENSE", "docs/../main.go", "unknown"):
            with self.subTest(path=path):
                self.assertFalse(pr_ci.docs_only([file("README.md"), file(path)]))

    def test_renames_check_old_and_new_paths(self):
        self.assertFalse(pr_ci.docs_only([file("docs/old.md", "renamed", previous_filename="main.go")]))
        self.assertFalse(pr_ci.docs_only([file("docs/old.md", "renamed")]))
        self.assertTrue(pr_ci.docs_only([file("docs/new.md", "renamed", previous_filename="docs/old.md")]))

    def test_empty_or_unknown_change_is_not_docs(self):
        self.assertFalse(pr_ci.docs_only([]))
        self.assertFalse(pr_ci.docs_only([file("README.md", "unexpected")]))
        self.assertTrue(pr_ci.docs_only([file("docs/old.md", "removed")]))

    def test_entire_pr_is_paginated_not_just_latest_commit(self):
        pr = dict(PR, changed_files=101)
        docs = [file(f"docs/{i}.md") for i in range(100)]
        client = FakeClient([pr, docs, [file("main.go")], pr])
        self.assertTrue(pr_ci.classify(client, REPO, PR))
        self.assertIn("page=2", client.calls[2][1])

    def test_complete_docs_pr(self):
        client = FakeClient([PR, [file("README.md")], PR])
        self.assertFalse(pr_ci.classify(client, REPO, PR))

    def test_truncated_file_list_fails_closed(self):
        client = FakeClient([dict(PR, changed_files=2), [file("README.md")], dict(PR, changed_files=2)])
        with self.assertRaisesRegex(ValueError, "incomplete"):
            pr_ci.classify(client, REPO, PR)

    def test_api_limit_requires_ci(self):
        client = FakeClient([dict(PR, changed_files=3001)])
        self.assertTrue(pr_ci.classify(client, REPO, PR))
        self.assertEqual(len(client.calls), 1)

    def test_duplicate_files_fail_closed(self):
        pr = dict(PR, changed_files=2)
        client = FakeClient([pr, [file("README.md"), file("README.md")], pr])
        with self.assertRaisesRegex(ValueError, "incomplete"):
            pr_ci.classify(client, REPO, PR)

    def test_head_change_is_rejected(self):
        newer = copy.deepcopy(PR)
        newer["head"]["sha"] = "c" * 40
        with self.assertRaisesRegex(ValueError, "changed"):
            pr_ci.classify(FakeClient([newer]), REPO, PR)

    def test_base_advance_before_run_is_allowed(self):
        newer = copy.deepcopy(PR)
        newer["base"]["sha"] = "c" * 40
        self.assertFalse(pr_ci.classify(FakeClient([newer, [file("README.md")], newer]), REPO, PR))

    def test_base_change_during_paging_is_rejected(self):
        newer = copy.deepcopy(PR)
        newer["base"]["sha"] = "c" * 40
        with self.assertRaisesRegex(ValueError, "changed"):
            pr_ci.classify(FakeClient([PR, [file("README.md")], newer]), REPO, PR)

    def test_api_failure_is_not_a_docs_skip(self):
        with self.assertRaises(pr_ci.APIError):
            pr_ci.classify(FakeClient([pr_ci.APIError("GET", "/pulls", 403)]), REPO, PR)


class PiperTests(unittest.TestCase):
    def test_trigger_preserves_pr_context_and_build_definition(self):
        pr = copy.deepcopy(PR)
        payload = pr_ci.trigger_payload(CONFIG, pr, {"GITHUB_RUN_ID": "123"})
        self.assertEqual(payload["commit"], SHA)
        self.assertEqual(payload["branch"], "feature")
        self.assertEqual(payload["meta_data"]["github_base_branch"], "main")
        self.assertNotIn("env", payload["overrides"])
        self.assertNotIn("build", payload["overrides"])
        self.assertNotIn("members", payload["overrides"])

    def test_gate_does_not_trigger_release_validation(self):
        pr = copy.deepcopy(PR)
        pr["base"]["ref"] = "release-1.0.0"
        with self.assertRaisesRegex(ValueError, "main PR"):
            pr_ci.trigger_payload(CONFIG, pr, {})

    def test_fork_and_draft_code_do_not_get_privileged_builds(self):
        for kind in ("fork", "draft"):
            pr = copy.deepcopy(PR)
            if kind == "fork":
                pr["head"]["repo"]["full_name"] = "outside/component"
            else:
                pr["draft"] = True
            with self.assertRaises(ValueError):
                pr_ci.trigger_payload(CONFIG, pr, {})

    def test_wrong_bundle_cannot_be_triggered(self):
        client = FakeClient([{"ob_pipeline": "other"}])
        with self.assertRaisesRegex(ValueError, "pipeline mismatch"):
            pr_ci.prepare(client, {"bundle_id": "id", "ob_pipeline": "expected"}, {"overrides": {}})
        self.assertEqual([c[0] for c in client.calls], ["GET"])

    def test_previous_commit_success_is_rejected(self):
        piper = FakeClient([{"number": 5, "commit": "c" * 40, "state": "passed"}])
        with self.assertRaisesRegex(ValueError, "head commit"):
            pr_ci.wait_result(piper, FakeClient([PR]), "/bundle", REPO, PR, 5, 10)

    def test_build_submission_is_not_success(self):
        piper = FakeClient([{"number": 5, "commit": SHA, "state": s} for s in ("running", "passed")])
        pr_ci.wait_result(piper, FakeClient([PR, PR, PR]), "/bundle", REPO, PR, 5, 10, sleep=lambda _: None)
        self.assertEqual(len(piper.calls), 2)

    def test_failed_cancelled_and_skipped_builds_fail(self):
        for state in pr_ci.TERMINAL - {"passed"}:
            with self.subTest(state=state), self.assertRaises(ValueError):
                pr_ci.wait_result(FakeClient([{"number": 5, "commit": SHA, "state": state}]),
                                  FakeClient([PR, PR]), "/bundle", REPO, PR, 5, 10)

    def test_timeout_fails(self):
        times = iter([0, 11])
        with self.assertRaises(TimeoutError):
            pr_ci.wait_result(FakeClient([]), FakeClient([]), "/bundle", REPO, PR, 5, 10,
                              clock=lambda: next(times))

    def test_cleanup_only_cancels_its_active_build(self):
        piper = FakeClient([{"number": 5, "commit": SHA, "state": "running"}, {}])
        pr_ci.cancel_active(piper, {"ob_pipeline": "pr-component"}, "/bundle", 5, SHA)
        self.assertEqual(piper.calls[-1][:2], ("POST", "/api/ob/pipelines/pr-component/builds/5/cancel"))
        piper = FakeClient([{"number": 5, "commit": SHA, "state": "passed"}])
        pr_ci.cancel_active(piper, {"ob_pipeline": "pr-component"}, "/bundle", 5, SHA)
        self.assertEqual(len(piper.calls), 1)


class GateTests(unittest.TestCase):
    def needs(self, required, result, changes="success"):
        return {"changes": {"result": changes, "outputs": {"needs_ci": required}}, "piper": {"result": result}}

    def test_docs_succeed_without_build(self):
        self.assertIn("no Buildkite", pr_ci.gate(self.needs("false", "skipped")))

    def test_code_requires_actual_success(self):
        self.assertIn("passed", pr_ci.gate(self.needs("true", "success")))
        for result in ("failure", "skipped", "cancelled", ""):
            with self.subTest(result=result), self.assertRaises(ValueError):
                pr_ci.gate(self.needs("true", result))

    def test_detection_failure_or_missing_output_blocks_merge(self):
        for changes in ("failure", "cancelled", "skipped"):
            with self.assertRaises(ValueError):
                pr_ci.gate(self.needs("false", "skipped", changes))
        with self.assertRaises(ValueError):
            pr_ci.gate(self.needs("", "skipped"))

    def test_unexpected_docs_build_does_not_hide_a_routing_bug(self):
        with self.assertRaises(ValueError):
            pr_ci.gate(self.needs("false", "success"))


class ClientTests(unittest.TestCase):
    def test_trigger_transport_failure_is_never_retried(self):
        client = pr_ci.Client("https://piper.example", "test-token")
        with patch.object(client.opener, "open", side_effect=urllib.error.URLError("lost response")) as call:
            with self.assertRaises(pr_ci.APIError):
                client.request("POST", "/api/bundles/id/trigger", {})
        self.assertEqual(call.call_count, 1)

    def test_read_failure_is_retried(self):
        client = pr_ci.Client("https://piper.example", "test-token")
        with patch.object(client.opener, "open", side_effect=[urllib.error.URLError("temporary"), io.BytesIO(b'{}')]) as call, patch("pr_ci.time.sleep"):
            self.assertEqual(client.request("GET", "/api/bundles/id"), {})
        self.assertEqual(call.call_count, 2)

    def test_credentials_cannot_be_redirected(self):
        self.assertIsNone(pr_ci.NoRedirect().redirect_request(None, None, 302, "", {}, "https://elsewhere"))
        for origin in ("http://piper.example", "https://user:pass@piper.example", "https://piper.example/api"):
            with self.assertRaises(ValueError):
                pr_ci.Client(origin, "token")


def recorded_run(number=5, **changes):
    record = {
        "id": "piper-run", "bundle_id": CONFIG["bundle_id"], "ob_pipeline": CONFIG["ob_pipeline"],
        "repository": REPO, "ob_build": number, "created_at": f"2026-11-17T00:{number:02d}:00Z",
        "trigger": pr_ci.trigger_payload(CONFIG, PR, {"GITHUB_RUN_ID": "123", "GITHUB_RUN_ATTEMPT": "1"}),
    }
    record.update(changes)
    return record


def build(number=5, state="passed", **extra):
    return {"number": number, "commit": SHA, "branch": "feature", "state": state, **extra}


class ReuseTests(unittest.TestCase):
    def start(self, piper, attempt="2"):
        return pr_ci.start_or_reuse(piper, CONFIG, PR,
                                   {"GITHUB_RUN_ID": "123", "GITHUB_RUN_ATTEMPT": attempt},
                                   "/api/bundles/bundle", {})

    def test_successful_retry_reuses_original_build_without_post(self):
        piper = FakeClient([[recorded_run()], build()])
        actual, created = self.start(piper)
        self.assertEqual(actual["number"], 5)
        self.assertFalse(created)
        self.assertTrue(all(call[0] == "GET" for call in piper.calls))

    def test_running_or_failed_build_is_reused_not_retriggered(self):
        for state in ("running", "failed", "canceled", "skipped"):
            with self.subTest(state=state):
                piper = FakeClient([[recorded_run()], build(state=state)])
                actual, created = self.start(piper)
                self.assertFalse(created)
                self.assertEqual(actual["state"], state)
                self.assertTrue(all(call[0] == "GET" for call in piper.calls))

    def test_new_run_creates_one_build(self):
        piper = FakeClient([[], build(state="queued")])
        actual, created = self.start(piper, "1")
        self.assertTrue(created)
        self.assertEqual(actual["number"], 5)
        self.assertEqual([call[0] for call in piper.calls], ["GET", "POST"])

    def test_different_identity_never_reuses_a_success(self):
        for change in ("commit", "pr", "workflow", "branch", "base", "repository", "pipeline", "bundle"):
            run = recorded_run()
            if change == "commit":
                run["trigger"]["commit"] = "c" * 40
            elif change == "pr":
                run["trigger"]["meta_data"]["github_pr"] = "43"
            elif change == "workflow":
                run["trigger"]["meta_data"]["github_run_id"] = "124"
            elif change == "branch":
                run["trigger"]["branch"] = "other"
            elif change == "base":
                run["trigger"]["meta_data"]["github_base_branch"] = "release-1.0.0"
            else:
                run[{"repository": "repository", "pipeline": "ob_pipeline", "bundle": "bundle_id"}[change]] = "other"
            with self.subTest(change=change):
                self.assertFalse(pr_ci.matches_run(run, CONFIG, PR, "123"))

    def test_latest_failed_run_is_not_replaced_by_older_success(self):
        piper = FakeClient([[recorded_run(5), recorded_run(6)], build(6, "failed")])
        actual, created = self.start(piper)
        self.assertEqual(actual["number"], 6)
        self.assertFalse(created)

    def test_ambiguous_existing_trigger_cannot_create_another_build(self):
        piper = FakeClient([[recorded_run(ob_build=0)]])
        with self.assertRaisesRegex(ValueError, "no confirmed build"):
            self.start(piper)
        self.assertEqual(len(piper.calls), 1)

    def test_incomplete_history_fails_closed_on_rerun(self):
        other = recorded_run()
        other["trigger"]["meta_data"]["github_run_id"] = "999"
        piper = FakeClient([[other] * pr_ci.RUN_LIMIT])
        with self.assertRaisesRegex(ValueError, "bounded"):
            self.start(piper)
        self.assertEqual(len(piper.calls), 1)

    def test_failed_lookup_does_not_trigger_a_duplicate(self):
        piper = FakeClient([pr_ci.APIError("GET", "/runs", 503)])
        with self.assertRaises(pr_ci.APIError):
            self.start(piper)
        self.assertEqual(len(piper.calls), 1)

    def test_build_response_must_match_the_recorded_commit(self):
        with self.assertRaisesRegex(ValueError, "head commit"):
            self.start(FakeClient([[recorded_run()], build(commit="d" * 40)]))


class SyncTests(unittest.TestCase):
    def setUp(self):
        self.event = {"state": "success", "context": CONFIG["status_context"], "sha": SHA,
                      "target_url": "https://obedients.k8s.rebellions.in/orgs/main/pipelines/pr-component/builds/5"}
        self.workflow = {"id": 123, "head_sha": SHA, "path": CONFIG["workflow_file"],
                         "event": "pull_request", "status": "completed", "conclusion": "failure", "run_attempt": 1}
        self.jobs = {"total_count": 1, "jobs": [{"id": 456, "name": "Piper PR validation",
                      "status": "completed", "conclusion": "failure", "steps": [{"name": pr_ci.PIPER_STEP}]}]}

    def clients(self, *, pr=None, state="passed", workflow=None, jobs=None, run=None):
        run = run or recorded_run()
        return (FakeClient([[run], [run], build(state=state)]),
                FakeClient([pr or PR, workflow or self.workflow, jobs or self.jobs, pr or PR, {}]))

    def test_success_event_rechecks_actions_and_never_starts_piper(self):
        piper, github = self.clients()
        self.assertTrue(pr_ci.sync_results(piper, github, CONFIG, self.event))
        self.assertTrue(all(call[0] == "GET" for call in piper.calls))
        self.assertEqual(github.calls[-1][:2], ("POST", f"/repos/{REPO}/actions/jobs/456/rerun"))

    def test_completion_event_recovers_status_completion_race(self):
        piper, github = self.clients()
        self.assertTrue(pr_ci.sync_results(piper, github, CONFIG, {"workflow_run": self.workflow}))

    def test_unrelated_or_forged_status_is_ignored(self):
        for changes in ({"state": "failure"}, {"context": "unrelated"},
                        {"target_url": "https://elsewhere/builds/5"},
                        {"target_url": self.event["target_url"].replace("pr-component", "other")},
                        {"sha": "short"}):
            with self.subTest(changes=changes):
                piper, github = FakeClient([]), FakeClient([])
                self.assertFalse(pr_ci.sync_results(piper, github, CONFIG, {**self.event, **changes}))
                self.assertFalse(piper.calls or github.calls)

    def test_old_workflow_protocol_cannot_be_restarted(self):
        run = recorded_run()
        del run["trigger"]["meta_data"]["github_pr_ci_version"]
        piper, github = FakeClient([[run]]), FakeClient([])
        self.assertFalse(pr_ci.sync_results(piper, github, CONFIG, self.event))
        self.assertFalse(github.calls)

    def test_piper_is_authoritative_even_if_event_says_success(self):
        for state in ("running", "failed", "canceled", "skipped"):
            with self.subTest(state=state):
                piper, github = self.clients(state=state)
                self.assertFalse(pr_ci.sync_results(piper, github, CONFIG, self.event))
                self.assertTrue(all(call[0] == "GET" for call in github.calls))

    def test_closed_fork_draft_or_stale_pr_is_not_restarted(self):
        for change in ("closed", "fork", "draft", "sha", "base"):
            pr = copy.deepcopy(PR)
            if change == "closed":
                pr["state"] = "closed"
            elif change == "fork":
                pr["head"]["repo"]["full_name"] = "other/component"
            elif change == "draft":
                pr["draft"] = True
            elif change == "sha":
                pr["head"]["sha"] = "c" * 40
            else:
                pr["base"]["ref"] = "release-1.0.0"
            with self.subTest(change=change):
                piper, github = self.clients(pr=pr)
                self.assertFalse(pr_ci.sync_results(piper, github, CONFIG, self.event))
                self.assertEqual(len(github.calls), 1)

    def test_active_cancelled_successful_or_repeated_workflow_is_not_restarted(self):
        for fields in ({"status": "in_progress"}, {"conclusion": "success"},
                       {"conclusion": "cancelled"}, {"run_attempt": 3},
                       {"head_sha": "c" * 40}, {"path": ".github/workflows/other.yaml"}):
            with self.subTest(fields=fields):
                piper, github = self.clients(workflow={**self.workflow, **fields})
                self.assertFalse(pr_ci.sync_results(piper, github, CONFIG, self.event))
                self.assertTrue(all(call[0] == "GET" for call in github.calls))

    def test_legacy_unconditional_trigger_job_is_not_restarted(self):
        self.jobs["jobs"][0]["steps"] = [{"name": "Run the existing Piper bundle for this PR head commit"}]
        piper, github = self.clients()
        self.assertFalse(pr_ci.sync_results(piper, github, CONFIG, self.event))
        self.assertTrue(all(call[0] == "GET" for call in github.calls))

    def test_an_unrelated_image_scan_failure_does_not_rerun_piper(self):
        self.jobs["jobs"][0]["conclusion"] = "success"
        self.jobs["jobs"].append({"id": 789, "name": "image-scan / Build and scan image",
                                  "status": "completed", "conclusion": "failure"})
        self.jobs["total_count"] = 2
        piper, github = self.clients()
        self.assertFalse(pr_ci.sync_results(piper, github, CONFIG, self.event))
        self.assertTrue(all(call[0] == "GET" for call in github.calls))

    def test_status_for_old_build_cannot_hide_new_failure(self):
        piper = FakeClient([[recorded_run()], [recorded_run(), recorded_run(6)], build(6, "failed")])
        github = FakeClient([PR])
        self.assertFalse(pr_ci.sync_results(piper, github, CONFIG, self.event))

    def test_pr_push_before_rerun_write_is_rejected(self):
        piper, github = self.clients()
        newer = copy.deepcopy(PR)
        newer["head"]["sha"] = "c" * 40
        github.replies[3] = newer
        with self.assertRaisesRegex(ValueError, "changed"):
            pr_ci.sync_results(piper, github, CONFIG, self.event)
        self.assertTrue(all(call[0] == "GET" for call in github.calls))


if __name__ == "__main__":
    unittest.main()
