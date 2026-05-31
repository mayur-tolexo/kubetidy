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
- [x] Cluster Efficiency Score (single shareable number)
- [x] Dollar-first rightsizing recommendations with confidence + evidence
- [x] `--explain <workload>` — full derivation of every number
- [x] `--output json` — stable schema for automation
- [x] **Action-ready `Recommendation` type** (carries the patch that *would* be applied)
- [ ] krew plugin manifest + Homebrew tap + `curl | sh` install
- [ ] README demo GIF

## 🔜 Phase 2 — The real product begins: safe, reversible action (M3)

> Close the gap between "here's the waste" and "it's fixed" — reversibly.

- [ ] `kubetidy diff` — render the exact resource patch
- [ ] `kubetidy pr` — open a GitOps pull request with the diff and `$/mo` delta in the title
- [ ] GitHub Action / CI cost-guardrail ("this PR adds $400/mo")
- [ ] Slack weekly digest (retention: puts kubetidy in a loop teams already run)
- [ ] Opt-in anonymized score submission → "State of Cluster Waste" data report

## 🔭 Phase 3 — Accountability + autonomy (M4–6)

> Where the value and the $1B valuations actually are.

- [ ] Guarded `apply` with auto-rollback on SLO regression
- [ ] Tier 2: OpenCost integration for precise allocated cost
- [ ] Accountability: per-team showback + budgets
- [ ] Kubernetes Operator + Recommendation CRDs (continuous, in-cluster)
- [ ] Multi-cluster aggregation
- [ ] Cleanup detectors (orphaned services/PVCs, idle namespaces, zombie workloads)

## 💼 Commercial (open-core)

Free forever: `scan`, recommend, `--explain`, single-cluster, PR diff.
Paid control plane: continuous SaaS, multi-cluster, governance/showback, guarded auto-apply.

---

### Design principles that won't change

1. **Read-only by default. Reversible always.** No surprise mutations, ever.
2. **Every number shows its work.** Trust is the moat.
3. **Works on any cluster.** Graceful degradation; never a hard dependency for a first run.
4. **Kubernetes-native & open.** client-go, Prometheus, OpenCost — open stack, open governance.
