# Changelog

All notable changes to kubetidy are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html) (pre-1.0: minor = features, patch =
fixes/UX).

## [Unreleased] — v0.1.1

The operator's Tier‑0 history now actually reaches a scan, and confidence is honest about how
much data backs each recommendation. Found and fixed against a real cluster.

### Fixed
- **Operator history was silently discarded.** `UsageProfile` declares a status subresource, so
  the operator's `Save` (plain `Create`/`Update`) persisted empty profile shells — sample count,
  per-container percentiles and histograms were dropped. `Save` now writes the status subresource
  (`UpdateStatus`). Profiles created before the fix refill in place on the next ticks.
- **Scan ignored the operator.** `selectUsageProvider` never selected the operator tier; it went
  straight from Prometheus to the metrics-server snapshot. It now prefers the operator's history
  (Tier 0) when `UsageProfile`s exist, with a per-workload metrics-server fallback so coverage is
  not lost while the operator warms up.
- Sub-hour windows render as `12m` instead of a broken-looking `0h`.

### Changed
- **Confidence is now data-maturity gated.** Time-series tiers (operator/Prometheus/OpenCost)
  earn their high base only as window *and* sample coverage accumulate, so a freshly-installed
  operator with two readings reads **low**, not a false ~85%.
- **Confidence is shown as bands** (`▒ low · ▓ med · █ high`) instead of a false-precision
  percentage; the exact % remains under `--explain`.
- The scan `data:` banner reports the tier that actually backed the findings (dominant tier),
  not the provider's declared tier — honest during operator warm-up.

## [0.1.0] — 2026-06-01

Initial public release.

### Added
- `kubectl tidy scan` — read-only efficiency score, dollar waste, and rightsizing
  recommendations with confidence + evidence; `--explain`, `--output json`.
- `kubectl tidy diff` — exact, reversible `kubectl patch` per recommendation.
- `kubectl tidy pr` — GitOps change set (patch files + Markdown PR body).
- `kubectl tidy init` / `uninstall` — install/remove the CRDs + read-only operator from
  binary-embedded manifests.
- Data ladder: metrics-server snapshot, Prometheus (auto-detected, Tier 1), and the in-cluster
  **kubetidy operator** (Tier 0 history with no Prometheus).
- **OpenCost** integration for precise allocated cost (auto-detected, or `--opencost-url`; Tier 2).
- CRDs: `UsageProfile`, `ClusterUsageSummary`, `Recommendation`.
- Install via **krew**, `curl | sh`, or pre-built archives; tagged GitHub release pipeline
  (GoReleaser) with an auto-rendered krew manifest.

[Unreleased]: https://github.com/mayur-tolexo/kubetidy/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/mayur-tolexo/kubetidy/releases/tag/v0.1.0
