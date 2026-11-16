# Contributing

Build and test changes from the repository root.

## Before you start

Use Linux, GNU Make, and the Go toolchain required by [go.mod](go.mod). Use [versions.mk](versions.mk) for pinned build tools.
Tests using the race detector also need a C compiler.

## Build and test

```bash
make build
make test
```

See the [Makefile](Makefile) for additional checks and container build targets.
Container builds require Docker with Buildx. Deployment checks need a suitable
Kubernetes cluster and NPU hardware.

## Submit a change

Describe the user-visible change, include relevant validation, and update any
affected guide or example. Keep README changes brief and place task-specific
instructions in the linked guides.

## Maintainer resources

- [Validate a release candidate](docs/rc-validation.md)
- [Check daemon API compatibility](api/README.md)
- [Review repository workflows](.github/workflows)

[Back to the README](README.md)
