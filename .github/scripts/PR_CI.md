# PR CI gate

For pull requests targeting `main`, `PR CI Gate` replaces the required external
Piper PR CI status. GitHub Actions always classifies the complete PR diff.

Only known root documentation files (README, CHANGELOG, CONTRIBUTING,
CODE_OF_CONDUCT and SECURITY) and prose/image files under `docs/` can skip CI.
Executable examples, manifests, dependency files, build scripts and workflow
changes still run the existing checks. Renames must have documentation paths on
both sides. Incomplete file lists and API failures cannot produce a docs-only pass.

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
2. In the `protect-main` ruleset, replace the external `[component] PR CI`
   required context with `PR CI Gate`, provided by GitHub Actions (app 15368).
   Preserve all other required checks and rules. Existing expensive GitHub jobs
   are skipped by job conditions for docs-only PRs; the workflow itself runs.
3. Disable `build_pull_requests` on the linked Obedients PR pipeline. Actions is
   then the only automatic main PR dispatcher; leaving both enabled creates
   duplicate builds. Keep the existing Piper bundle, YAML, schedules and manual
   release triggers. Release workflows and release rulesets are outside this change.

Coordinate activation with the workflow rollout so existing PRs are not left
waiting for a check that cannot run. Before retrying an ambiguous trigger failure,
inspect Piper Runs and the recorded GitHub run ID for an existing build.

## Local validation

```sh
python3 -m unittest discover -s .github/scripts -p 'test_pr_ci.py' -v
```

`pr_ci.py run --dry-run` validates the mapping and previews the unchanged bundle;
it never creates a Buildkite build.
