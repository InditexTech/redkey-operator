<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Change Log
All notable changes to this project will be documented in this file.

Format is based on [Keep a Changelog](http://keepachangelog.com/).

## [Unreleased] - dd.mm.yyyy
### Added
- E2E spec `persistence` (`test/e2e/persistence_config_test.go`) covering the persistence ownership: an ephemeral cluster gets a single `appendonly no` / `save ""` from Robin, a persistent cluster without persistence directives runs with the Redis defaults, and a persistent cluster switches AOF on → off → on through `spec.config` without repeated directives or data loss (#113).
### Fixed
### Changed
- Persistence directives are now owned by exactly one layer (requires the matching Robin change, which no longer forces `appendonly yes` on persistent clusters). Removed the redundant `appendonly no` / `save ""` from the ephemeral samples (`config/samples/{ephemeral,auth,replicas}`) and from the default `config` of `charts/redkey`. The E2E framework sets persistence explicitly on persistent clusters (`WithPVC` disables it, `WithPersistence` enables AOF) and no longer sets it on ephemeral ones (#113).
- Docs: the persistence parameters in `cluster-configuration.md` (who writes `appendonly`/`save`, Redis defaults, and changing persistence on a running cluster, including the standalone AOF caveat), the persistent example in `redkey-deployment.md`, the standalone storage notes, and `backup-configuration.md`, which wrongly stated that the operator configures RDB backups every 15 minutes (#113).

## [0.2.1] - 22.10.2026
### Added
- "Robin Version" stat in the Redkey Robin Grafana dashboard (Overview row), based on Robin's new `redkey_robin_build_info` metric, and a "Build Info Metrics" section in `docs/metrics.md`.
- Go native fuzz tests for `mergeMeta` and `needsNewConfig`, plus a `make fuzz` target.
- `--version` flag for the operator manager binary, printing the version and exiting.
- E2E tests for persistent clusters with `deletePVC: false`: two consecutive Rolling N+1 upgrades (with and without replicas), upgrade + scale 3→0→3 + upgrade, scale 3→0→3 restoring data (with and without replicas), scale 3→0→3 with `deletePVC: true`, and scale 3→1→3 reusing retained PVCs. The E2E framework gains `ClusterOptions.WithDeletePVC` and `ClusterOptions.WithPersistence` (keeps AOF enabled, which the default E2E config disables, so data really survives pod restarts).
### Fixed
- Bump `golang.org/x/net` to v0.61.0 to fix HTTP/2 vulnerabilities GO-2026-6603, GO-2026-6610, GO-2026-6611, GO-2026-6612 and GO-2026-6617.
- Configure the controller-runtime logger in the e2e suite to stop the `log.SetLogger(...) was never called` warning when setup exceeds 30s (#100).
### Changed
- Bump Go to 1.27.2, controller-runtime to v0.25.2, `k8s.io/*` to v0.37.1 and the remaining direct dependencies to their latest versions.
- Bump controller-gen to v0.22.0 (markers migrated to `+required`/`+optional`) and golangci-lint to v2.14.0.
- Pin envtest and the kind e2e/chaos cluster to Kubernetes 1.33 (production target) and bump kind to v0.33.0.
- The operator version is now injected at build time from the Makefile `VERSION` (`-ldflags -X main.version`, `dev` for plain `go build`) instead of the hardcoded `USER_AGENT_VERSION` constant; it feeds `redkey_operator_build_info` and the API client `User-Agent`.
### Removed
- Unused `RedkeyReconciler.createIfNotExists` helper and its dedicated unit test.
- `USER_AGENT_VERSION` check from the release workflow, and the unused `version-next`/`version-set` Makefile targets.
