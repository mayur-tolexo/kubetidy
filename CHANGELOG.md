# Changelog

All notable changes to kubetidy are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html) (pre-1.0: minor = features, patch =
fixes/UX).

## [Unreleased]

### Fixed
- **OpenCost now actually produces cost with the bundled stack.** OpenCost computes allocation
  cost by reading metrics back from Prometheus — its own exported pricing metrics plus
  kube-state-metrics — but the bundled Prometheus scraped only cAdvisor, so OpenCost returned
  zero cost and kubetidy fell back to derived pricing. The bundled Prometheus now also scrapes
  OpenCost's `/metrics` and a **bundled minimal kube-state-metrics** (deployed alongside it),
  closing the loop so Tier-2 cost computes (allow ~10–15 min after install to warm up).
- **PromQL queries no longer break on workload names with dots/regex metacharacters.** Names like
  `rbd.csi.ceph.com-nodeplugin` produced an `unknown escape sequence` error (the `\.` from regex
  escaping is invalid inside a PromQL double-quoted string), so those workloads were skipped.
  Backslashes are now escaped for the PromQL string context.
- **Auto-detected Prometheus/OpenCost now reachable from `scan`.** Detection returned an
  in-cluster Service DNS name (`*.svc`) that doesn't resolve on the user's machine, so every
  query failed with "no such host" and the scan silently reported a misleading `100/100, $0`.
  `scan` now reaches an auto-detected Prometheus or OpenCost through the **Kubernetes API server
  proxy** (reusing the kubeconfig's address + credentials — works wherever kubectl works, no
  port-forward), and **validates reachability** before committing to Tier 1/Tier 2; an
  unreachable endpoint falls back to the operator / metrics-server / derived pricing with a clear
  note instead of producing empty results. An explicit `--prometheus-url` / `--opencost-url` is
  still used directly.

### Added
- **`kubetidy init --with-opencost`** — deploy a complete Tier-2 cost stack into the cluster from
  embedded manifests, so scans get precise allocated cost out of the box. OpenCost needs
  Prometheus, so when no external one is given `--with-opencost` **also deploys a minimal bundled
  Prometheus** (monitoring namespace, scrapes kubelet/cAdvisor) — one command, no prerequisites.
  Point at your own Prometheus instead with `--prometheus-url` (then the bundle is skipped).
- **`kubetidy init --with-prometheus`** — deploy just the bundled Prometheus (unlocks Tier-1
  history on a cluster with no Prometheus), without OpenCost.
- `kubetidy uninstall --with-opencost` / `--with-prometheus` remove those components again (both
  off by default, so a user's own OpenCost/Prometheus is never touched; the shared `monitoring`
  namespace is preserved). `init --print` includes whatever `--with-*` flags select, for GitOps.
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
