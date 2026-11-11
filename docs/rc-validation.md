# RC validation through piper

After the release kit publishes an RC, `release.yaml` calls
`POST /api/bundles/{id}/trigger` and waits for piper's existing OB verdict and
delivery of `rc-validation` to the RC commit. It does not rebuild the RC image
or implement a separate test-report verdict.

## Setup

- GitHub Actions secret `PIPER_TOKEN`: an piper bearer token whose owner has
  `builds.run` and `integrations.write`. piper's GitHub integration must be able
  to write commit statuses to `RBLN-SW/rbln-metrics-exporter`.
- Variable `PIPER_URL` defaults to `https://piper.k8s.rebellions.in`.
- `.github/scripts/piper-rc.json` records bundle `f10a3ea4-ddf6-4283-b851-0a98afa70942` and OB
  pipeline `pr-metrics-exporter`. `PIPER_RC_BUNDLE_ID` can override the ID;
  the helper verifies the execution repository and OB link.
- `CLOUD_COMPONENT_HARBOR_REGISTRY` is the same host/project used by the release
  kit to publish images. Registry credentials remain managed by piper/OB.
- Merge the bootstrap into the branch/commit OB will check out, sync that
  repository in piper, and switch the OB entry point to that bootstrap.
  It must forward `?run=$PIPER_RUN`. The helper fails before triggering if the
  recorded bootstrap or OB entry point is not ready.
- Release branches need this workflow, helper and bootstrap before creating a
  new RC. An old RC tag still contains its old workflow.
- `RC_VALIDATION_SENTINEL_REF` optionally overrides the harness ref on every
  member; sync that ref's catalog in piper first.
- `RC_VALIDATION_OPERATOR_REF` optionally selects a different Operator chart
  checkout through member chart overrides. Omit it to retain piper's saved chart.

## Request contract

`build.enabled=false` disables both the build step and commit-SHA image-tag
rewriting. `force_env=true` applies this RC's explicit image values over saved
member Env. `commit_status={enabled:true, context:"rc-validation"}` and
`status_target={repository, sha}` let piper report directly to the release
commit. Saved PR/nightly definitions and other runs are unchanged.

The exact RC SHA and release branch are passed to piper. The published RC tag
is applied only to the component under test; the registered Operator/driver/
compiler/cluster baseline is preserved.

| Published image | Sentinel environment fields |
|---|---|
| `rbln-metrics-exporter` | `SENTINEL_METRICS_EXPORTER_IMAGE_{REGISTRY,REPOSITORY,TAG,PULL_POLICY}` |

## Result and retries

The trigger's HTTP 201 means a build was created. The helper records its build
URL and run ID, then reads that exact build/run until piper has delivered the
matching final `rc-validation` status. Success exits zero; the existing OB
failure/error result exits nonzero. Cleanup/notification failures are part of
the normal piper/OB result. No coverage-count or custom RC verdict is added.

A trigger request is sent once. On an ambiguous timeout, inspect piper Runs
before retrying; a repeated POST creates another build. A wait timeout does
not cancel the OB build. Use piper to inspect, cancel or retry an existing run;
do not rerun image publication just to retry validation.

For a read-only payload check with the same release environment:
`python3 .github/scripts/piper_rc.py --dry-run`. This reads piper configuration
and calls preview only. The offline unittest suite intercepts all requests.
