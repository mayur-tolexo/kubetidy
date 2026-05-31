# kubetidy

> **See your cluster's wasted dollars in 20 seconds — no Prometheus required.**

`kubetidy` is a Kubernetes-native CLI that scores your cluster's efficiency, quantifies
wasted spend in **real dollars**, and gives you evidence-backed, **action-ready** rightsizing
recommendations — and tells you *why*.

It is read-only and safe to run anywhere. It works with just the Kubernetes API +
metrics-server (no Prometheus needed), and gets sharper when Prometheus is available.

```text
kubetidy scan  ·  context: prod-us-east  ·  tier: 1 (Prometheus)

  Cluster Efficiency Score:  41 / 100   ▇▇▇▇░░░░░░
  Rightsizing waste:         $7,420 / month

  TOP RECOMMENDATIONS
  ─────────────────────────────────────────────────────────
  checkout-api    cpu 2000m→320m   mem 4Gi→1.1Gi   -$210/mo   conf 96%
    evidence: P95 cpu 280m, max mem 0.9Gi over 14d · 1.2M samples
  search-indexer  cpu 4000m→1500m  mem 8Gi→6Gi     -$180/mo   conf 91%

  Run `kubetidy scan --explain checkout-api` for the full math.
```

---

## Why kubetidy?

Most clusters run at **~10% CPU / ~20% memory utilization** — you are paying for capacity
your workloads never use. Plenty of tools *find* that waste; the hard part is **trusting**
and **acting on** the recommendations. kubetidy is built around trust:

- **Works on any cluster, instantly.** Tier 0 needs only the Kubernetes API +
  metrics-server. No agents, no Prometheus dependency to get started.
- **Dollars, not millicores.** A single, shareable **Cluster Efficiency Score** and a
  monthly dollar figure.
- **Every number shows its work.** `--explain` reveals the exact query, window, sample
  count, variance, and policy behind each recommendation.
- **Read-only and reversible by design.** kubetidy never mutates your cluster. `diff` prints
  the exact, reversible `kubectl patch` you would run — you review it, run it, or discard it.

## The three-tier data ladder

kubetidy auto-detects the best data source available and stamps every finding with the tier
that proved it. It never fails hard — if Prometheus is missing it falls back to
metrics-server; if that is missing it falls back to static analysis.

| Tier | Needs | You get | Confidence |
|------|-------|---------|------------|
| 0 | K8s API + metrics-server | live usage snapshot, cost from node pricing | low–medium |
| 1 | + Prometheus | historical P50/P95/max over a window | high |
| 2 | + OpenCost *(coming)* | precise allocated cost | high |

## Install

> Packaging via **krew** (`kubectl krew install tidy`), **Homebrew**, and `curl | sh` is on
> the [roadmap](ROADMAP.md). For now, build from source.

Requires **Go 1.26+**.

```sh
git clone https://github.com/mayur-tolexo/kubetidy.git
cd kubetidy
make build          # produces ./bin/kubetidy and ./bin/kubectl-tidy
```

Put the binaries on your `PATH`. As soon as `kubectl-tidy` is on `PATH`, the kubectl plugin
form works:

```sh
sudo cp ./bin/kubetidy ./bin/kubectl-tidy /usr/local/bin/
kubectl tidy scan
```

kubetidy inherits your current kubeconfig context and namespace, exactly like any other
kubectl plugin.

## Usage

kubetidy ships as a single binary with two faces — use whichever you prefer:

- `kubectl tidy <command>` (kubectl plugin form)
- `kubetidy <command>` (standalone)

### `scan` — score, dollars, and recommendations

```sh
kubectl tidy scan                       # scan current context, all namespaces
kubectl tidy scan -n payments           # scope to one namespace
kubectl tidy scan --output json         # machine-readable, stable schema
kubectl tidy scan --explain checkout    # full derivation for one workload
kubectl tidy scan --prometheus-url URL  # force Tier 1 (Prometheus)
kubectl tidy scan --top 10              # limit recommendations shown
```

### `diff` — the exact, reversible patch

`diff` prints, for each recommendation, the precise `kubectl patch` command that would apply
it, with the monthly saving. It is **read-only** — kubetidy never runs the patch; you review,
run, or discard it.

```sh
kubectl tidy diff                       # patches for every recommendation
kubectl tidy diff --explain checkout    # just the patch for one workload
kubectl tidy diff --top 5               # only the top 5 by savings
```

Example output:

```text
# checkout-api (Deployment/shop/checkout-api) · saves $210/mo · conf 96%
kubectl patch deployment checkout-api -n shop --type=strategic -p '{"spec":{"template":{"spec":{"containers":[{"name":"checkout-api","resources":{"requests":{"cpu":"320m","memory":"1126Mi"}}}}]}}}}'
```

### Common flags

| Flag | Applies to | Description |
|------|-----------|-------------|
| `-n, --namespace` | scan, diff | Namespace to scan (default: all) |
| `--context` | scan, diff | kubeconfig context to use |
| `--prometheus-url` | scan, diff | Prometheus base URL (forces Tier 1) |
| `--window` | scan, diff | Prometheus lookback window (default `14d`) |
| `--explain` | scan, diff | Focus on a single workload |
| `--top` | scan, diff | Max recommendations to show |
| `--cpu-cost` / `--mem-cost` | scan, diff | Override $/core-month and $/GiB-month |
| `-o, --output` | scan | `table` (default) or `json` |

## Rightsizing policy (defaults)

- **CPU request** = P95 + 15% headroom; **no CPU limit** by default (avoids throttling).
- **Memory request** = max + 15% headroom (memory OOMs, so we use max, not a percentile);
  **memory limit** = request (Guaranteed QoS).

All defaults are surfaced in `--explain` and overridable. The number is never a black box.

## Status

🚧 **MVP under active development.** `scan` and `diff` work today. See the
[roadmap](ROADMAP.md) and the [design spec](docs/superpowers/specs/2026-05-31-kubetidy-design.md)
for what is next (GitOps PRs, OpenCost cost, multi-cluster, an operator).

## Contributing

kubetidy is open stack, Kubernetes-native, and built for contribution. Start with
[good first issues](https://github.com/mayur-tolexo/kubetidy/labels/good%20first%20issue) and
read [CONTRIBUTING.md](CONTRIBUTING.md). The pure-logic packages (`rightsizer`, `costmodel`,
`score`, `patch`) are the easiest, highest-value place to start.

## License

[Apache-2.0](LICENSE).
