# kubetidy Roadmap

kubetidy's thesis: **detection is commoditized; safe action and accountability are not.**
The CLI is the wedge that earns trust and distribution. The action loop is the product.

This roadmap is intentionally public and open for discussion — please weigh in via
[issues](https://github.com/kubetidy/kubetidy/issues) and [discussions](https://github.com/kubetidy/kubetidy/discussions).

---

## ✅ Phase 1 — MVP: the read-only wedge (M1–2)

> Win trust and stars. Prove the dollar number, explainably, on any cluster.

- [x] `kubectl tidy scan` / `kubetidy scan` — read-only
- [x] Three-tier data ladder: Tier 0 (metrics-server, zero-dep) + Tier 1 (Prometheus)
- [x] **Prometheus auto-detection** — scans upgrade to Tier 1 with zero config
- [x] Tier-0 snapshot safety (extra headroom, request floors, downsize-only)
- [x] Cluster Efficiency Score (single shareable number)
- [x] Dollar-first rightsizing recommendations with confidence + evidence
- [x] `--explain <workload>` — full derivation of every number
- [x] `--output json` — stable schema for automation
- [x] **Action-ready `Recommendation` type** (carries the patch that *would* be applied)
- [x] One-command local demo (`make e2e` / `make e2e-prom`) + animated terminal demo
- [x] krew plugin manifest + `curl | sh` install + tagged GitHub release pipeline (GoReleaser
      cross-platform binaries, checksums, auto-rendered krew manifest). *(Homebrew tap deferred.)*
- [x] README demo GIF (real Tier-1 scan + diff, recorded with VHS via `make demo-gif`)

## 🔜 Phase 2 — The real product begins: safe, reversible action (M3)

> Close the gap between "here's the waste" and "it's fixed" — reversibly.

- [x] `kubetidy diff` — render the exact, reversible `kubectl patch` per recommendation
- [x] `kubetidy pr` — GitOps change set: per-recommendation patch files + a Markdown PR body
      that leads with the `$/mo` delta (apply via Argo CD / Flux / `kubectl`)
- [x] One-command Prometheus deploy (`make prometheus`) to unlock Tier 1 anywhere
- [ ] GitHub Action / CI cost-guardrail ("this PR adds $400/mo")
- [ ] Slack weekly digest (retention: puts kubetidy in a loop teams already run)
- [ ] Opt-in anonymized score submission → "State of Cluster Waste" data report

## 🔭 Phase 3 — Accountability + autonomy (M4–6)

> Where the value and the $1B valuations actually are.

- [x] **kubetidy operator** — read-only in-cluster usage historian; records decaying
      histograms into `UsageProfile` CRDs so scans get Prometheus-grade recommendations with
      no Prometheus (the real Tier 0). The first, safe increment of the operator. See
      [docs/design/operator.md](docs/design/operator.md).
- [x] **`kubectl tidy init`** — install the CRD + operator from manifests embedded in the
      binary; no manual `kubectl apply`.
- [ ] Guarded `apply` with auto-rollback on SLO regression
- [x] Tier 2: OpenCost integration for precise allocated cost (auto-detected, or `--opencost-url`)
- [x] **Recommendation CRDs** — the operator writes a per-workload `Recommendation` (the LLM
      target; rules-engine source today). Multi-cluster aggregation still to come.
- [x] **Confidence that grows with data** — recommendations are graded low/med/high and gated
      on data maturity, so warm-up history isn't passed off as high-confidence.
- [ ] Accountability: per-team showback + budgets
- [ ] Multi-cluster aggregation (continuous, in-cluster)
- [ ] Cleanup detectors (orphaned services/PVCs, idle namespaces, zombie workloads)
- [ ] Interactive `scan -i` TUI to browse/filter recommendations and drill into `--explain`

## 💼 Commercial (open-core)

Free forever: `scan`, recommend, `--explain`, single-cluster, PR diff.
Paid control plane: continuous SaaS, multi-cluster, governance/showback, guarded auto-apply.

---

### Design principles that won't change

1. **Read-only by default. Reversible always.** No surprise mutations, ever.
2. **Every number shows its work.** Trust is the moat.
3. **Works on any cluster.** Graceful degradation; never a hard dependency for a first run.
4. **Kubernetes-native & open.** client-go, Prometheus, OpenCost — open stack, open governance.
