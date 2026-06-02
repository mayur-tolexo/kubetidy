# Changelog

All notable changes to kubetidy are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html) (pre-1.0: minor = features, patch =
fixes/UX).

## [Unreleased]

### Added
- **`kubetidy init --with-opencost`** — optionally deploy OpenCost into the cluster (namespace,
  RBAC, deployment, service; embedded manifests) so scans get precise Tier-2 cost out of the box.
  OpenCost reads usage from Prometheus; point it with `--prometheus-url` (defaults to
  `http://prometheus-server.monitoring.svc:80`). `kubetidy uninstall --with-opencost` removes it
  again (off by default, so a user's own OpenCost is never touched). `init --print --with-opencost`
  emits the manifests for GitOps instead of applying them.
- **`kubetidy cost`** — the CI cost-guardrail. Prices CPU/memory requests in manifests (no
  cluster) and, with `--base`/`--head`, reports the monthly $ a change adds or saves
  ("this change adds $88/mo"); `--fail-over <budget>` fails CI on a net increase. Example
  GitHub Actions workflow in `docs/examples/cost-guardrail.yml`.

## [0.1.2] — 2026-06-02

Scan output rebuilt around making every recommendation understandable, plus a real memory
safety fix, an interactive browser, and a new cleanup command. Driven by real-cluster feedback.

### Added
- **`kubetidy scan -i`** — an interactive terminal UI to browse and filter recommendations and
  drill into the full `--explain` detail, without leaving the terminal (Charm bubbletea).
- **`kubetidy sweep`** — find removable junk (the literal tidy): orphaned Services (selector
  matches no pods), unused PVCs (with estimated `$/mo` storage cost), idle namespaces, and
  zombie (scaled-to-zero) Deployments/StatefulSets. Read-only; `table`/`json` output.
- **Card per recommendation.** Each finding is its own block: a bordered table of the observed
  **avg / p95 / p99 / peak** for CPU and memory beside the `request → proposed` change, with the
  workload name on its own line (no more truncated/again-wrapped wide table).
- **Confidence score % inline** next to the band (e.g. `▒ low 34%`), so you can watch it climb
  as history accumulates.
- Data **provenance in the banner** (`5h history, ~202 samples/workload`) and a per-card `basis`
  line, so "what was measured, over how long, from how many samples" is always visible.
- `--explain` gains a "why this recommendation" block: requested vs the full observed
  distribution vs proposed, with an over-allocation verdict.

### Changed
- **OOM-safe memory sizing.** Memory is the dangerous resource and a short window can miss the
  true peak, so the memory headroom now scales inversely with data maturity — a young history
  keeps a large cushion (up to peak × ~2), tightening to peak + 15% only once a representative
  window of samples backs it. CPU stays lean (under-sizing it only throttles). New
  `Policy.MemoryImmatureSafety`.
- `model.Percentiles` gains `Avg` + `P99`; providers populate them (Prometheus `avg_over_time` +
  `quantile_over_time(0.99)`; operator histogram `Mean()` + p99; `UsageProfile` CRD
  `MetricHistory` gains additive `avg`/`p99` — operator redeploy needed to populate).

## [0.1.1] — 2026-06-01

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

[Unreleased]: https://github.com/mayur-tolexo/kubetidy/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/mayur-tolexo/kubetidy/releases/tag/v0.1.2
[0.1.1]: https://github.com/mayur-tolexo/kubetidy/releases/tag/v0.1.1
[0.1.0]: https://github.com/mayur-tolexo/kubetidy/releases/tag/v0.1.0
