# PR CI gate

For pull requests targeting `main`, `PR CI Gate (required checks)` replaces the required external
Piper PR CI status. GitHub Actions always classifies the complete PR diff.

Only explicitly recognized documentation and community templates can skip the
expensive checks:

- Root README, CHANGELOG, CONTRIBUTING, CODE_OF_CONDUCT and SECURITY files.
- Prose and document images under `docs/`.
- PR/Issue template text files in the root or `.github/`, and PR template text
  files under `PULL_REQUEST_TEMPLATE/` in the root, `docs/` or `.github/`.
- Markdown/text and YAML issue forms or chooser configuration under
  `.github/ISSUE_TEMPLATE/`.

Source, tests, dependencies, Dockerfiles, Makefiles, Helm/CRDs/deployment YAML,
executable examples, `.github/workflows`, Actions, CI scripts and `.buildkite`
still run CI. Unknown paths run CI too. A mixed documentation/code PR runs CI.
Renames must match the exclusion policy on both sides. Incomplete file lists
and API failures cannot produce a documentation-only pass.

Change detection and PR title validation remain lightweight checks. The Gate
aggregates the original required jobs listed in `pr-ci.json` (`gate_jobs`) and
Piper. For code changes every required job must succeed; for documentation and
templates the expensive jobs may be skipped, but PR title validation must pass.
Missing, failed or cancelled required results never pass the Gate. Image scans
that were advisory remain outside the Gate. The aggregate check has a new name so
older Piper-only `PR CI Gate` successes cannot satisfy the expanded requirement.

For code changes, the gate calls the existing Piper bundle configured in
`pr-ci.json`, using the full PR head SHA, and waits for that exact build to pass.
Draft and fork code PRs cannot start privileged internal CI. Mark an internal PR
ready for review, or review fork changes on an internal branch. Cancellation,
failed/skipped builds and a stale PR head do not satisfy the gate. A trigger POST
is never retried automatically because its response may be lost after creation.

`PIPER_TOKEN` must allow bundle reads, previews, triggering and cancellation.
The Piper job uses the existing internal GitHub runner to reach Piper.

## Activation

1. Validate this workflow and script on a main-targeting PR.
2. After the aggregate Gate is merged to `main`, update `protect-main` to
   require `PR CI Gate (required checks)`, provided by GitHub Actions (app 15368). Replace both the
   external `[component] PR CI` context and the conditional lint/build/test
   contexts covered by `gate_jobs`. Keep PR title validation and any unrelated
   checks/rules. Reusable-workflow check names can differ when their caller is
   skipped; retaining those conditional contexts can leave documentation PRs
   waiting for checks that are not produced. The Gate enforces the same required
   code checks under one stable name. Do not remove their individual requirements
   before this aggregate Gate is deployed.
3. Disable `build_pull_requests` on the linked Obedients PR pipeline. Actions is
   then the only automatic main PR dispatcher; leaving both enabled creates
   duplicate builds. Keep the existing Piper bundle, YAML, schedules and manual
   release triggers. Release workflows and release rulesets are outside this change.

Coordinate activation with the workflow rollout so existing PRs are not left
waiting for a check that cannot run. Before retrying an ambiguous trigger failure,
inspect Piper Runs and the recorded GitHub run ID for an existing build.

## Retrying and synchronizing results

An Actions rerun reuses the latest Piper build recorded for the same workflow
run ID, PR, repository, bundle, branch and full head SHA. Running builds are
observed; passed builds complete immediately. Failed or cancelled builds stay
failed until they are retried successfully in Piper. A new commit or a different
workflow run never borrows another run's success. An incomplete history lookup
fails closed instead of creating a duplicate, and cancelling an observer does
not cancel an independently retried Piper build.

Piper reports the whole bundle using the existing component PR CI context for
this run. `piper-pr-status.yaml` observes successful status updates and completed
failed PR workflows. It verifies the current PR and the actual Piper build before
rerunning only `Piper PR validation` and its dependent Gate. It does not rerun
image scans or other checks, and never creates a Piper build itself. Automatic
rechecks are capped at two per workflow run to prevent loops; manual Actions
reruns still use the same recorded Piper build.

The callback runs default-branch code with Actions write permission. It becomes
active after this workflow is merged to `main`. Older workflows without the reuse
protocol are not automatically restarted, since they would create a fresh build.

## Local validation

```sh
python3 -m unittest discover -s .github/scripts -p 'test_pr_ci.py' -v
```

`pr_ci.py run --dry-run` validates the mapping and previews the unchanged bundle;
it never creates a Buildkite build.
