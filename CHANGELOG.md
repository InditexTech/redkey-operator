<!--
SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL S.A. (INDITEX S.A.)

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Change Log
All notable changes to this project will be documented in this file.

Format is based on [Keep a Changelog](http://keepachangelog.com/).

## [Unreleased] - dd.mm.yyyy
### Added
- "Robin Version" stat in the Redkey Robin Grafana dashboard (Overview row), based on Robin's new `redkey_robin_build_info` metric, and a "Build Info Metrics" section in `docs/metrics.md`.
- Go native fuzz tests for `mergeMeta` and `needsNewConfig`, plus a `make fuzz` target.
- E2E tests for persistent clusters with `deletePVC: false`: two consecutive Rolling N+1 upgrades (with and without replicas), upgrade + scale 3→0→3 + upgrade, scale 3→0→3 restoring data (with and without replicas), scale 3→0→3 with `deletePVC: true`, and scale 3→1→3 reusing retained PVCs. The E2E framework gains `ClusterOptions.WithDeletePVC` and `ClusterOptions.WithPersistence` (keeps AOF enabled, which the default E2E config disables, so data really survives pod restarts).
### Fixed
- Bump `golang.org/x/net` to v0.61.0 to fix HTTP/2 vulnerabilities GO-2026-6603, GO-2026-6610, GO-2026-6611, GO-2026-6612 and GO-2026-6617.
- Configure the controller-runtime logger in the e2e suite to stop the `log.SetLogger(...) was never called` warning when setup exceeds 30s (#100).
### Changed
- Bump Go to 1.27.2, controller-runtime to v0.25.2, `k8s.io/*` to v0.37.1 and the remaining direct dependencies to their latest versions.
- Bump controller-gen to v0.22.0 (markers migrated to `+required`/`+optional`) and golangci-lint to v2.14.0.
- Pin envtest and the kind e2e/chaos cluster to Kubernetes 1.33 (production target) and bump kind to v0.33.0.
### Removed
- Unused `RedkeyReconciler.createIfNotExists` helper and its dedicated unit test.
