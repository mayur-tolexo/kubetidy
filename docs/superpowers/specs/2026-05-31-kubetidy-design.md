# kubetidy — Design Spec (MVP)

**Status:** Approved for MVP implementation
**Date:** 2026-05-31
**Working name:** `kubetidy` / `kubectl tidy`

---

## 1. One-line vision

> "Tell me what I can safely remove or optimize, how much money it saves, and why."

A Kubernetes-native CLI that scores a cluster's efficiency, quantifies wasted spend in
dollars, and produces evidence-backed, action-ready rightsizing recommendations — starting
read-only, designed from day one to grow into safe, reversible *action*.

## 2. Strategic thesis (why this shape)

The market evidence is unambiguous and shaped this design:

- **The pain is real and large.** Production clusters run at ~8–10% CPU / ~20% memory
  utilization; overprovisioning is the #1 cause of K8s overspend (CNCF), and "workload
  optimization & waste reduction" is the #1 FinOps priority (State of FinOps 2025).
- **Detection is commoditized; action is not.** krr (Prometheus-based recommendations,
  ~4.6k stars, free), Goldilocks, VPA, Kubecost/OpenCost all *find* waste. The trust gap is
  the real bottleneck: 89% prioritize rightsizing but only ~17–27% trust automation to
  apply it; VPA (free, native) runs in production at <1% of orgs because it's "too
  dangerous." Recommendations "accumulate in a dashboard nobody looks at."
- **Therefore:** the CLI is a *wedge* to win trust and distribution; the *business* is in
  closing the gap between "here's the waste" and "it's safely fixed," plus accountability.

**Design consequence:** the MVP is a read-only scanner, but its core `Recommendation` type
is **action-ready** — the post-MVP action layer (GitOps PR / guarded apply) is a new
*consumer* of the same object, not a rewrite.

## 3. Differentiation (vs krr / kubecost), built into the MVP

1. **Zero-dependency Tier 0.** Works with only the Kubernetes API + metrics-server. krr
   *requires* Prometheus. "Works on any cluster, instantly" is the install-funnel edge.
2. **Cluster Efficiency Score.** A single, shareable, comparable headline number. No peer
   tool has this. It is the screenshot that travels.
3. **Dollar-first.** Output is `$/month`, not raw millicores.
4. **Trust apparatus as a feature.** `--explain` shows the full derivation (query, window,
   sample count, variance, policy). Trust — not detection — is the moat we start building.

## 4. The three-tier data ladder (core architecture)

The scan never fails hard. It discovers the best available tier and stamps every finding
with the tier that proved it.

| Tier | Source | Unlocks | Confidence |
|------|--------|---------|-----------|
| 0 | K8s API + metrics-server | live usage snapshot, requests-vs-actual, cost from node pricing | low–medium |
| 1 | + Prometheus (auto-detected/flag) | historical P50/P95/max over a window | high |
| 2 | + OpenCost (auto-detected) | precise allocated cost (replaces derived) | high |

If only the K8s API is reachable: static analysis (missing requests/limits, absurd
request:limit ratios) labeled "no usage data — low confidence."

**MVP implements Tier 0 + Tier 1.** Tier 2 (OpenCost) is defined behind the `PriceProvider`
interface and deferred to a later phase.

## 5. Components (bounded Go packages, each independently testable)

```
cmd/kubetidy           main(); cobra commands (root, scan, version)
internal/model         domain types: Workload, Container, ResourceAmounts, UsageStats,
                       Recommendation, ScanResult, EvidenceTier, Confidence, Policy
internal/kube          kubeconfig/client-go setup; workload discovery
internal/usage         UsageProvider interface + metricsserver, prometheus implementations
internal/pricing       PriceProvider interface + config/default implementation
internal/rightsizer    PURE: UsageStats + Policy -> RecommendedResources
internal/costmodel     PURE: (current vs recommended) + pricing -> $/month
internal/score         PURE: ScanResult -> Efficiency Score (0..100) + breakdown
internal/report        renderers: TTY table, JSON, --explain
internal/scan          orchestrator/engine wiring the above into a ScanResult
internal/version       build/version metadata
```

**Data flow:** `cmd → scan.Engine → kube.Discover → usage.Provider → rightsizer →
costmodel → score → report`.

**Key interfaces:**

```go
type UsageProvider interface {
    Name() string
    Tier() model.EvidenceTier
    // Usage returns per-container usage stats for a workload over the window.
    Usage(ctx context.Context, w model.Workload) (map[string]model.UsageStats, error)
}

type PriceProvider interface {
    Name() string
    // ResourcePrice returns the price per CPU-core-month and per-GiB-month
    // attributable to the given workload's scheduling target.
    ResourcePrice(ctx context.Context, w model.Workload) (model.ResourcePrice, error)
}
```

`rightsizer`, `costmodel`, and `score` are **pure functions** (no I/O) — exhaustively
table-tested. Collectors are tested via interface fakes. `report` via golden files.

## 6. Rightsizing policy (opinionated defaults, configurable)

- **CPU request** = P95 + headroom (default 15%); **no CPU limit** by default (avoid
  throttling).
- **Memory request** = max + headroom (memory OOMs → use max, not P95); **memory limit** =
  request (Guaranteed QoS) by default.
- Defaults surfaced in output; overridable via flags. The number is never a black box.

## 7. Confidence model

A derived, reproducible function of: tier, window length, sample count, and variance.
Tier-1 / 14-day / low-variance → 95%+. Tier-0 single snapshot → capped ~60%. Shown in full
under `--explain`. Confidence is never cosmetic — one wrong high-confidence call burns trust
permanently.

## 8. Error handling

Every tier is optional and degrades independently: Prometheus down → fall back to
metrics-server → fall back to static. Partial failures are annotated per-finding, never
fatal. The scan always emits a report.

## 9. Output (the "aha" screenshot)

```
kubetidy scan  ·  context: prod-us-east  ·  tier: 1 (Prometheus, 14d)

  Cluster Efficiency Score:  41 / 100   ▇▇▇▇░░░░░░
  Rightsizing waste:         $7,420 / month

  TOP RECOMMENDATIONS
  ─────────────────────────────────────────────────────────
  checkout-api    cpu 2000m→320m   mem 4Gi→1.1Gi   -$210/mo   conf 96%
    evidence: P95 cpu 280m, max mem 0.9Gi over 14d · 1.2M samples
  ...
  Run `kubetidy scan --explain checkout-api` for the full math.
```

Also `--output json` (stable schema, for automation) and `--output table` (default).

## 10. Distribution — `kubectl tidy` as primary entry point

- One Go binary, installed on PATH as **`kubectl-tidy`** → `kubectl tidy scan` works via the
  kubectl plugin convention (kubectl execs `kubectl-<name>` from PATH).
- Also installed as **`kubetidy`** → standalone `kubetidy scan`.
- Cobra reads `os.Args[0]` to set the displayed root command name; subcommands behave
  identically either way.
- Inherits kubeconfig/context/namespace/auth automatically (runs like any kubectl plugin).
- **Distribution:** krew plugin manifest (`kubectl krew install tidy`) + Homebrew tap +
  `curl | sh` + GitHub Releases (goreleaser).

## 11. Testing & engineering standards

- TDD throughout; table-driven unit tests for pure packages; interface fakes for collectors;
  golden-file tests for `report`.
- `go vet`, `golangci-lint`, `gofmt` clean. CI on GitHub Actions (build + test + lint).
- Apache-2.0 license (CNCF-friendly). Conventional commits. `good first issue` labels.
- Clear package boundaries, dependency injection, no global state in core logic.

## 12. Explicit MVP scope boundaries (YAGNI)

**In:** read-only `scan` (Tier 0 + Tier 1), Efficiency Score, dollar waste, action-ready
rightsizing recommendations, `--explain`, JSON output, `kubectl tidy` + `kubetidy`
distribution.

**Out (deferred, designed-for not built):** operator, CRDs, multi-cluster, OpenCost (Tier 2),
cleanup detectors (orphaned services/PVCs/idle namespaces/zombie workloads), any actuation,
web UI, persistence/history.

## 13. Roadmap

See [`ROADMAP.md`](../../../ROADMAP.md). Summary:

- **M1–2 (MVP, this spec):** read-only scan + score + dollars + explain + krew/brew. Ship for stars.
- **M3 (the real product begins):** `kubetidy diff` → opens a GitOps PR with the resource
  diff and `$/mo` delta in the title. Reversible by construction = the trust unlock. Plus a
  GitHub Action / CI cost-guardrail and Slack weekly digest for weekly-usage retention.
- **M4–6:** guarded `apply` with auto-rollback on SLO regression; accountability layer
  (per-team showback, budgets); OpenCost (Tier 2); first SaaS control-plane bits
  (continuous, multi-cluster).

## 14. Go-to-market (summary)

- **OSS wedge:** zero-dependency CLI + shareable Efficiency Score + shocking `$/mo` number.
  README GIF first. Launch on Show HN ("see your cluster's wasted $ in 20s, no Prometheus
  needed"), r/kubernetes, CNCF/K8s Slack, LinkedIn FinOps community. Distribute via
  krew + brew + curl.
- **Data flywheel:** opt-in anonymized score submission → recurring "State of Cluster Waste"
  report (the engine Cast AI used to own the conversation) → earned media + dollar-number
  credibility.
- **Retention:** GitHub Action cost-guardrail + Slack digest put kubetidy in a loop teams
  already run.
- **Land-and-expand:** free OSS CLI proves the dollar number bottom-up → FinOps/platform
  lead takes it upstairs → paid control plane (continuous, multi-cluster, governance,
  guarded auto-apply). Pricing: per-cluster, with %-of-verified-savings for enterprise.
- **Open-core line (stated up front):** scan/recommend/explain/single-cluster/PR-diff are
  free forever; continuous SaaS, multi-cluster, governance, guarded auto-apply are paid.
- **Metrics that matter:** scan→re-scan retention, % repos running the CI guardrail,
  anonymized scores submitted, design-partner count — not raw stars.
