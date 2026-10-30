# Contributing to RBLN Metrics Exporter

Thank you for your interest in contributing. This document covers the
licensing terms that apply to contributions and the basic workflow.

## License of contributions

This project is licensed under the [Apache License 2.0](LICENSE). By
submitting a contribution (pull request, patch, or otherwise) you agree that
it is licensed under the Apache License 2.0, as described in Section 5 of the
license: "any Contribution intentionally submitted for inclusion in the Work
by You to the Licensor shall be under the terms and conditions of this
License, without any additional terms or conditions."

## Developer Certificate of Origin

Every commit must be signed off to certify that you wrote the code or
otherwise have the right to submit it under the project license, as set out
in the [Developer Certificate of Origin 1.1](https://developercertificate.org/).

Add the sign-off with `git commit -s`, which appends a line like:

```
Signed-off-by: Your Name <your.email@example.com>
```

Use your real name and a working email address.

## Source file headers

- Files written for this project carry the Rebellions Inc. Apache-2.0 header.
  New Go, shell, and Python files must include it.
- Files derived from upstream projects keep the upstream copyright header
  unchanged, followed by a "Modifications Copyright" notice. Do not remove or
  edit upstream headers. If you bring in code from another project, keep its
  header, add the modification notice, and list the source in `NOTICE`.
- Run `make license-check` before opening a pull request.

## Third-party dependencies

Only dependencies under permissive licenses (Apache-2.0, MIT, BSD, ISC) are
accepted. After changing `go.mod`, regenerate `THIRD_PARTY_NOTICES` with
`make third-party-notices`.

## Pull requests

- Use Conventional Commits for the PR title (see the pull request template).
- Run `make code-check` and the unit tests locally.
- One logical change per pull request.
