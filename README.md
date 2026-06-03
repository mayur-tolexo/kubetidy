<p align="center">
  <img src="docs/assets/logo.svg" alt="kubetidy" width="380">
</p>

<p align="center">
  <strong>See your cluster's wasted dollars in 20 seconds — no Prometheus required.</strong>
</p>

`kubetidy` is a Kubernetes-native CLI that scores your cluster's efficiency, quantifies
wasted spend in **real dollars**, and gives you evidence-backed, **action-ready** rightsizing
recommendations — and tells you *why*.

It is read-only and safe to run anywhere. Install its tiny in-cluster operator and you get
Prometheus-grade recommendations with **no Prometheus** at all.

<p align="center">
  <img src="docs/assets/demo.gif" alt="kubectl tidy scan and diff demo" width="820">
</p>

---

## Why kubetidy?

Most clusters run at **~10% CPU / ~20% memory utilization** — you are paying for capacity
your workloads never use. Plenty of tools *find* that waste; the hard part is **trusting**
and **acting on** the recommendations. kubetidy is built around trust:

- **Works on any cluster.** Install the operator for high-confidence history, or point it at
  Prometheus — no hard dependency to get a first answer.
- **Dollars, not millicores.** A single, shareable **Cluster Efficiency Score** and a
  monthly dollar figure.
- **Every number shows its work.** `--explain` reveals the exact query, window, sample
  count, variance, and policy behind each recommendation.
- **Confidence you can trust.** Each recommendation is graded **▒ low · ▓ med · █ high**, and
  the grade *grows as history accumulates* — a freshly-installed operator with a couple of
  readings reads **low**, never a false "85%". It only earns **high** once a real window of
  samples backs it (`--explain` shows the exact %).
- **Read-only and reversible by design.** kubetidy never mutates your cluster. `diff` prints
  the exact, reversible `kubectl patch`; `pr` writes a GitOps change set you review and merge.

## The data ladder

kubetidy auto-detects the best data source available and stamps every finding with the tier
that proved it. It never fails hard — it degrades to whatever is present.

There are two tiers — the difference is **how the dollars are priced**:

| Tier | Needs | Usage data | Cost basis |
|------|-------|------------|------------|
| 1 | K8s API + metrics-server **or** Prometheus | live snapshot (metrics-server) or historical P50/P95/max (Prometheus / kubetidy operator) | **derived** node pricing (blended `$/core`, `$/GiB`, refined by instance type) |
| 2 | Prometheus + **OpenCost** | historical P50/P95/max | **precise allocated cost** from OpenCost (spot / reserved / committed-use discounts included) |

Tier 1 works on **any cluster** with no cost dependency — the dollar figure is derived from
blended cloud pricing (override with `--cpu-cost` / `--mem-cost`). Tier 2 kicks in when an
in-cluster **OpenCost** is present (it needs Prometheus, which is why it sits above Tier 1):
kubetidy auto-detects it and replaces the derived prices with real allocated cost. Point at one
explicitly with `--opencost-url`, or let kubetidy deploy the whole stack (OpenCost **and** a
Prometheus if you lack one) with `kubectl tidy init --with-opencost` (see
[setup](#one-command-setup-kubectl-tidy-init)).

Within Tier 1, kubetidy auto-detects the best **usage** source — Prometheus or the kubetidy
operator (historical, high-confidence) over a bare metrics-server snapshot (a conservative
fallback) — and stamps every finding with what proved it. No flags required; it never fails
hard, degrading to whatever is present.

## Install

Pick whichever fits. All three install the same single binary, usable as both `kubetidy` and
the `kubectl tidy` plugin.

**krew** (the kubectl plugin manager) — recommended for `kubectl tidy`:

```sh
# until kubetidy lands in the central krew-index, install from the release manifest:
kubectl krew install --manifest-url \
  https://github.com/mayur-tolexo/kubetidy/releases/latest/download/kubetidy.yaml
kubectl tidy scan
```

**curl | sh** — standalone binary, no Go toolchain needed (Linux/macOS, amd64/arm64):

```sh
curl -fsSL https://raw.githubusercontent.com/mayur-tolexo/kubetidy/main/install.sh | sh
```

It downloads the latest release, verifies its checksum, and installs both `kubetidy` and
`kubectl-tidy` to `/usr/local/bin` (or `~/.local/bin`). Override with `KUBETIDY_VERSION`,
`KUBETIDY_BIN_DIR`, or `KUBETIDY_NO_PLUGIN=1`.

**Pre-built archives** — grab a tarball/zip for your platform from the
[releases page](https://github.com/mayur-tolexo/kubetidy/releases), extract, and put
`kubetidy` on your `PATH` (copy it to `kubectl-tidy` too for the plugin form).

**From source** (requires **Go 1.26+**):

```sh
git clone https://github.com/mayur-tolexo/kubetidy.git
cd kubetidy
make build          # produces ./bin/kubetidy and ./bin/kubectl-tidy
sudo cp ./bin/kubetidy ./bin/kubectl-tidy /usr/local/bin/
```

As soon as `kubectl-tidy` is on your `PATH`, `kubectl tidy ...` works. kubetidy inherits your
current kubeconfig context and namespace, exactly like any other kubectl plugin.

## One-command setup: `kubectl tidy init`

kubetidy installs its in-cluster components (the `UsageProfile` CRD and the operator) from
manifests **embedded in the binary** — no hunting for YAML, no `kubectl apply -f`:

```sh
kubectl tidy init                 # install the CRD + operator (server-side apply)
kubectl tidy init --crd-only      # just the CRD (e.g. GitOps manages the Deployment)
kubectl tidy init --print         # print the manifests instead of applying them
kubectl tidy init --image REPO/kubetidy-operator:TAG   # pin a custom operator image
kubectl tidy init --with-opencost # full Tier-2 cost stack: OpenCost + a bundled Prometheus
kubectl tidy init --with-prometheus # just a minimal Prometheus (unlocks Tier-1 history)
```

`init` applies the CRD first, waits for it to become Established, then deploys the operator.
It is idempotent — re-run it any time to converge the cluster to the embedded manifests.

### Precise cost out of the box: `--with-opencost`

`--with-opencost` deploys a complete **Tier-2 cost stack** from embedded manifests, so scans
report **precise allocated cost** from your cluster's actual node pricing (spot / reserved /
committed-use discounts included) instead of blended derived pricing:

- [OpenCost](https://www.opencost.io/) (namespace, RBAC, deployment, service) — kubetidy
  auto-detects it at `opencost.opencost.svc:9003`.
- OpenCost needs Prometheus, so **if you don't already run one, init also deploys a minimal
  bundled Prometheus** (the `monitoring` namespace, scraping kubelet/cAdvisor) at
  `prometheus-server.monitoring.svc:80`. One command, no prerequisites.

```sh
kubectl tidy init --with-opencost                          # OpenCost + bundled Prometheus
kubectl tidy init --with-opencost \
  --prometheus-url http://my-prometheus.monitoring.svc:9090 # use YOUR Prometheus (skips the bundle)
```

The bundled Prometheus is deliberately minimal: a single replica with **ephemeral storage** and
a 15-day window. It's perfect for getting cost working immediately; for durable, production-grade
metrics run your own Prometheus and pass `--prometheus-url`. Want only Tier-1 history (no cost)?
`kubectl tidy init --with-prometheus` deploys just the Prometheus.

`--print` includes whatever `--with-*` flags you pass, for GitOps. To remove these later:
`kubectl tidy uninstall --with-opencost --with-prometheus` (both off by default, so your own
OpenCost/Prometheus is never touched; the shared `monitoring` namespace is preserved).

To remove everything `init` created, use its inverse:

```sh
kubectl tidy uninstall              # delete the operator + all CRDs (and recorded data); prompts first
kubectl tidy uninstall --dry-run    # list exactly what would be removed; deletes nothing
kubectl tidy uninstall --yes        # skip the confirmation prompt
kubectl tidy uninstall --keep-crds  # remove only the operator; keep the CRDs and history
kubectl tidy uninstall --with-opencost  # also remove OpenCost installed via init --with-opencost
kubectl tidy uninstall --with-prometheus # also remove the bundled Prometheus (keeps the namespace)
kubectl tidy cleanup                # alias for uninstall
```

`uninstall` (alias `cleanup`) deletes the operator first (so it stops writing), then the CRDs
— which cascades to every recorded `UsageProfile`, `ClusterUsageSummary`, and
`Recommendation`. Use `--dry-run` to preview the exact objects (each marked present/absent)
without touching the cluster. It is idempotent: already-absent objects are skipped.

The operator runs from the published image `docker.io/mayurdas1991/kubetidy-operator:latest` —
a **multi-arch Linux image** (`linux/amd64` + `linux/arm64`) that runs in kind and in any
Kubernetes cluster. Maintainers publish it with `make operator-push` (after `docker login`;
requires Docker buildx). Pass `--image` to `init` to use a fork or a pinned version tag
instead.

## High-confidence scans without Prometheus — the operator (Tier 0)

A single `metrics-server` snapshot can't see peaks, so kubetidy is deliberately conservative
with it. The **kubetidy operator** fixes this *without* Prometheus: a tiny, read-only
in-cluster controller that continuously samples metrics-server, accumulates per-container usage
into decaying histograms (the technique the Vertical Pod Autoscaler uses), and stores the
result in `UsageProfile` custom resources. `scan` auto-detects it — Prometheus-grade
recommendations, zero external dependencies.

It is strictly read-only with respect to workloads: it observes and records, and **never evicts
or resizes anything** (that is what makes VPA risky; kubetidy does not do it).

```sh
kubectl tidy init               # installs CRD + operator
kubectl get usageprofiles -A    # inspect the recorded history
# give it a few minutes to accumulate, then:
kubectl tidy scan               # now runs at "data: 0 (kubetidy operator)"
```

For local kind testing the Makefile wraps the image build + deploy:

```sh
make operator-deploy
```

See [docs/design/operator.md](docs/design/operator.md) for the design.

## High-confidence scans with Prometheus (Tier 1)

Already run Prometheus? kubetidy auto-detects the common in-cluster service names, so plain
`kubectl tidy scan` upgrades to Tier 1 automatically. Because `scan` runs on your machine (where
in-cluster Service DNS doesn't resolve), it reaches an auto-detected Prometheus — and OpenCost —
**through the Kubernetes API server proxy**, reusing your kubeconfig's credentials: no
port-forward, works wherever `kubectl` works. It validates the endpoint answers before using it,
so a detected-but-unreachable service falls back cleanly instead of reporting empty results. Or
point it explicitly (used as-is, e.g. a `localhost` port-forward):

```sh
kubectl tidy scan --prometheus-url http://prometheus.monitoring.svc:9090
```

No Prometheus and want it for local testing? Deploy a minimal one and re-scan:

```sh
make prometheus       # deploy a tiny Prometheus (namespace: monitoring)
make demo-scan-prom   # scan the demo namespace at Tier 1
```

## Try it in 2 minutes (local kind cluster)

No real cluster handy? Spin up a throwaway [kind](https://kind.sigs.k8s.io/) cluster with
deliberately over-provisioned demo workloads and watch kubetidy flag the waste:

```sh
make e2e          # kind up → metrics-server → deploy demo → scan + diff
make e2e-prom     # same, plus Prometheus for a Tier-1 scan
make kind-down    # tear it all down
```

> Requires `kind` and `kubectl` on your PATH. The demo workloads use the `pause` image, so
> they request multiple cores/GiB while using almost nothing — exactly the waste kubetidy is
> built to surface.

## Make commands

Run `make help` to see everything. The common ones:

| Target | What it does |
|--------|--------------|
| `make build` | Build the binary as both `kubetidy` and `kubectl-tidy` into `./bin` |
| `make build-operator` | Build the kubetidy operator binary into `./bin` |
| `make install` | Build and copy both faces to `/usr/local/bin` |
| `make test` / `make test-race` | Run unit tests (optionally with the race detector) |
| `make cover` | Tests + total coverage; `make cover-html` for a browsable report |
| `make lint` | Run golangci-lint (installs it if missing) |
| `make check` | Full pre-PR gate: tests + vet + gofmt + lint |
| `make e2e` / `make e2e-prom` | Full local demo (with / without Prometheus) |
| `make demo-gif` | Re-record the README demo GIF with VHS (after `make e2e-prom`) |
| `make release-check` | Validate the GoReleaser config |
| `make release-snapshot` | Build all release archives + krew manifest into `./dist` (no publish) |
| `make operator-deploy` | Build + deploy the kubetidy operator (Tier 0, no Prometheus) |
| `make prometheus` | Deploy a minimal Prometheus (unlocks Tier 1) |
| `make kind-up` / `make kind-down` | Create / delete the kind cluster |
| `make clean` | Remove build and coverage output |

## Usage

kubetidy ships as a single binary with two faces — use whichever you prefer:

- `kubectl tidy <command>` (kubectl plugin form)
- `kubetidy <command>` (standalone)

Commands: **`scan`** (report), **`diff`** (reversible `kubectl patch` per recommendation),
**`sweep`** (find removable junk), **`cost`** (CI cost-guardrail), **`pr`** (a GitOps change set
— patch files + a Markdown PR body), **`init`** (install the CRD + operator), and `version`.

### `scan` — score, dollars, and recommendations

```sh
kubectl tidy scan                       # scan current context, all namespaces
kubectl tidy scan -n payments           # scope to one namespace
kubectl tidy scan --output json         # machine-readable, stable schema
kubectl tidy scan --explain checkout    # full derivation for one workload
kubectl tidy scan --prometheus-url URL  # force Tier 1 (Prometheus)
kubectl tidy scan --top 10              # limit recommendations shown
kubectl tidy scan -i                     # interactive TUI: browse, filter (/), drill in (enter)
```

`-i` opens a full-screen browser of the recommendations — arrow keys to move, `/` to filter,
`enter` to open the full `--explain` detail for a workload, `esc` to go back, `q` to quit.

### `diff` — the exact, reversible patch

`diff` prints, for each recommendation, the precise `kubectl patch` command that would apply
it, with the monthly saving. It is **read-only** — kubetidy never runs the patch.

```sh
kubectl tidy diff                       # patches for every recommendation
kubectl tidy diff --explain checkout    # just the patch for one workload
```

### `sweep` — find removable junk (the literal tidy)

`sweep` scans the cluster (read-only) for cleanup opportunities beyond rightsizing: Services
whose selector matches no pods, PersistentVolumeClaims nothing mounts (with an estimated
`$/mo`), namespaces with no running workloads, and Deployments/StatefulSets scaled to zero.

```sh
kubectl tidy sweep                      # all categories, cluster-wide
kubectl tidy sweep -n payments          # scope to one namespace
kubectl tidy sweep --storage-cost 0.08  # $/GiB-month for unused-PVC estimates
kubectl tidy sweep -o json              # machine-readable
```

```
  Found 9 cleanup opportunities · ~$24/mo reclaimable

  unused pvc (3)
    • logs/archive-2023    not mounted by any pod · 180Gi · ~$18/mo
  zombie workload (2)
    • ops/old-redis (StatefulSet)   scaled to 0 replicas
  …
  Read-only: review before deleting. kubetidy never deletes anything.
```

### `cost` — the CI cost-guardrail

`cost` prices the CPU/memory requests in Kubernetes manifests — **no cluster needed** — and, given
a before/after, reports the monthly `$` a change adds or saves. Drop it in CI to flag (or block)
PRs that quietly grow spend.

```sh
kubectl tidy cost ./manifests                          # total $/mo of a manifest set
kubectl tidy cost --base /tmp/base --head ./manifests  # diff: "this change adds $88/mo"
kubectl tidy cost --base B --head H --fail-over 200    # exit non-zero if it adds > $200/mo
kubectl tidy cost --base B --head H -o json            # machine-readable (for a PR comment)
```

```
kubetidy cost · base → head

  this change adds $88/mo  ($27/mo → $116/mo)

  WORKLOAD                                       BEFORE      AFTER      DELTA
  Deployment/shop/checkout-api                      $27       $109       +$82  (changed)
  StatefulSet/shop/cache                              —         $7        +$7  (added)
```

A ready-to-copy GitHub Actions workflow (comment the delta + enforce a budget) is in
[`docs/examples/cost-guardrail.yml`](docs/examples/cost-guardrail.yml).

### `pr` — a reviewable GitOps change set

`pr` turns the scan into something you can merge: one strategic-merge patch file per
recommendation, plus a Markdown PR body that leads with the monthly savings, a per-workload
table with evidence, and apply/revert instructions. kubetidy never commits, pushes, or
applies — you review and open the PR yourself (Argo CD / Flux or `kubectl` apply it).

```sh
kubectl tidy pr                      # write ./kubetidy-patches/ + print the PR body
kubectl tidy pr --out ./patches      # choose the output directory
kubectl tidy pr --body-out PR.md     # write the PR body to a file
kubectl tidy pr --include-grow       # also include under-provisioned ("grow") workloads
```

### Common flags

| Flag | Applies to | Description |
|------|-----------|-------------|
| `-n, --namespace` | scan, diff, pr | Namespace to scan (default: all) |
| `--context` | all | kubeconfig context to use |
| `--prometheus-url` | scan, diff, pr | Prometheus base URL (forces Tier 1) |
| `--window` | scan, diff, pr | Prometheus lookback window (default `14d`) |
| `--explain` | scan, diff | Focus on a single workload |
| `--top` | scan, diff, pr | Max recommendations to show/include |
| `--cpu-cost` / `--mem-cost` | scan, diff, pr | Override $/core-month and $/GiB-month |
| `-o, --output` | scan | `table` (default) or `json` |

## Rightsizing policy (defaults)

- **CPU request** = P95 + 15% headroom; **no CPU limit** by default (avoids throttling).
  Under-sizing CPU only throttles, so it stays lean.
- **Memory request** = peak + headroom, with **OOM safety**: memory is the dangerous resource
  (under-sizing kills the pod), and a short window may not have seen the true peak. So the
  memory buffer **scales with data maturity** — a young history keeps a large cushion
  (e.g. peak × 2), tightening to peak + 15% only once a representative window of samples backs
  it. **memory limit** = request (Guaranteed QoS).
- **Snapshot safety**: when only a single metrics-server snapshot is available, an extra
  buffer and request floors keep recommendations conservative.

Every scan shows its work: each recommendation is a card with the observed **avg / p95 / p99 /
peak** for CPU and memory beside the `request → proposed` change, the **window + sample count**
the numbers rest on, and a **confidence** grade (`▒ low · ▓ med · █ high` + score%) that grows
as history accumulates. `--explain <workload>` adds the full derivation. The number is never a
black box.

## Status

🚀 **v0.1.0 released** — install via [krew](#install), `curl | sh`, or pre-built archives.

`scan`, `diff`, `pr`, and `init` work today, with a read-only operator that records real
Tier‑0 history (no Prometheus), Prometheus auto-detection (Tier 1), OpenCost auto-detection for
precise cost (Tier 2), and confidence grading that scales with data maturity. See the
[changelog](CHANGELOG.md) for what's new, the [roadmap](ROADMAP.md) for what's next (guarded
apply, multi-cluster), and [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the design.

## Contributing

kubetidy is open stack, Kubernetes-native, and built for contribution. Start with
[good first issues](https://github.com/mayur-tolexo/kubetidy/labels/good%20first%20issue),
read [CONTRIBUTING.md](CONTRIBUTING.md), and skim
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the package layout. The pure-logic packages
(`rightsizer`, `costmodel`, `score`, `patch`, `histogram`) are the easiest, highest-value
place to start.

## License

[Apache-2.0](LICENSE).
