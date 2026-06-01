# Design: kubetidy operator — CRD-backed usage historian (Tier 0.5)

**Status:** Draft for review
**Author:** kubetidy maintainers
**Supersedes:** n/a · **Related:** [ROADMAP.md](../../ROADMAP.md) Phase 3, [ARCHITECTURE.md](../ARCHITECTURE.md)

---

## 1. Problem

kubetidy's headline promise is **"no Prometheus required."** But today the no-Prometheus path
(Tier 0, metrics-server) reads a **single point-in-time sample** — `metrics.k8s.io` is a gauge
with no history. A single sample cannot see peaks, so we apply a large safety buffer
(`SnapshotHeadroom +100%`) and request floors. The result is *safe but weak*: directional
recommendations, capped at ~60% confidence.

Real clusters show the gap: an idle-at-scan-time `redis` looks like it needs 7Mi; a workload
that spikes every Tuesday looks calm on a Monday snapshot. **Detection of peaks is the thing
that earns trust**, and a snapshot fundamentally can't provide it.

The data, however, is right there — metrics-server refreshes every ~15–60s. We throw away
every reading but the last. **If something accumulated those readings, the no-Prometheus path
would produce Prometheus-grade recommendations.**

## 2. Goals / Non-goals

**Goals**
- A read-only, in-cluster operator that continuously records per-container usage history and
  exposes it so `scan`/`diff`/`pr` produce high-confidence recommendations **without Prometheus**.
- Zero external infrastructure (no PVC, no TSDB, no Prometheus).
- Restart-safe; small footprint; never pages anyone.
- Be the first, safe increment of the Phase-3 operator (continuous mode, score history,
  cleanup-detector substrate).

**Non-goals (this iteration)**
- No mutation of workloads (no eviction/resize — that is what makes VPA dangerous; we don't do it).
- No multi-cluster aggregation (later; the CRD is the per-cluster building block).
- No replacement of Prometheus Tier 1 — the operator is Tier 0.5, between snapshot and Prometheus.
- No `Recommendation` CRD yet (optional follow-up); this spec covers the **usage history** CRD only.

## 3. Prior art

This is **VPA's recommender pattern**, minus the dangerous part. VPA polls the metrics API,
maintains **exponentially-decaying per-container histograms** (half-life ~24h), and checkpoints
them to a `VerticalPodAutoscalerCheckpoint` CRD — no Prometheus. It computes P90/P95 from the
histogram. We do the same collection, but emit kubetidy recommendations (dollars + PR) instead
of resizing pods. A read-only historian has none of VPA's eviction risk.

## 4. Architecture

Collection and consumption are split through a CRD:

```
 operator (Deployment)                 CRD: UsageProfile (one per workload)
 ─────────────────────                 ──────────────────────────────────
 ticker every scrapeInterval     write   status.containers[].cpu/mem histograms
   → list PodMetrics             ─────►   status.window, samples, lastUpdated
   → update in-mem histograms             (checkpoint of in-memory state)
   → checkpoint to CRD status
                                                     │ read
                                                     ▼
                                   kubectl tidy scan / diff / pr
                                     usage.Provider "operator" (Tier 0.5)
                                     auto-detected via CRD presence
```

- **In-memory histograms** are the live state (fast O(1) updates, fixed memory).
- **CRD status** is a periodic checkpoint: survives operator restarts (rehydrate on boot),
  needs no PVC, and is debuggable (`kubectl get usageprofiles`).
- **`scan` is unchanged in spirit**: a new `UsageProvider` reads the CRD and fills the same
  `model.UsageStats{P50,P95,Max,Window,Samples,Tier}` the pure `rightsizer` already consumes.
  Nothing in `rightsizer`/`costmodel`/`score` changes.

## 5. The CRD: `UsageProfile` (group `kubetidy.io`, `v1alpha1`)

One object per analyzed workload (Deployment/StatefulSet/DaemonSet), named after the workload,
in the workload's namespace.

```yaml
apiVersion: kubetidy.io/v1alpha1
kind: UsageProfile
metadata:
  name: checkout-api            # <kind>-<name>, lowercased; or use ownerRef
  namespace: shop
spec:
  targetRef:                    # which workload this profiles
    kind: Deployment
    name: checkout-api
status:
  observedSince: "2026-05-20T10:00:00Z"
  lastUpdated:   "2026-05-31T12:00:00Z"
  sampleCount:   38240
  window:        "11d"
  containers:
    - name: checkout-api
      cpu:   { p50Millicores: 110, p95Millicores: 280, maxMillicores: 410, histogram: "<encoded>" }
      memory:{ p50Bytes: 4.9e8, p95Bytes: 8.1e8, maxBytes: 9.4e8, histogram: "<encoded>" }
```

Design choices:
- **Status-heavy, spec-thin.** `spec.targetRef` is the only user/operator-set field; everything
  else is operator-maintained `status`. (Optionally drop spec entirely and use an `ownerReference`
  to the workload so GC cleans it up when the workload is deleted.)
- **`histogram` is the compact decaying-bucket state** (base64 of a fixed bucket array, see §6),
  so the operator can rehydrate exact P95 after restart — not just the last summary.
- **Printer columns**: WINDOW, SAMPLES, CPU-P95, MEM-P95, AGE for `kubectl get usageprofiles`.

## 6. Histogram model

Do **not** store raw timeseries (that reinvents Prometheus and explodes etcd). Store an
**exponentially-decaying bucketed histogram** per container per resource — VPA's `util/histogram`
blueprint:

- Fixed exponential buckets (e.g. CPU 1m→ ~64 cores, memory 1Mi→ ~128Gi; ~40–60 buckets each).
- Each sample adds a decaying weight; half-life ~24h so recent behavior dominates but a weekly
  spike still registers within the window.
- **Fixed memory** (~hundreds of bytes/container), O(1) update, P50/P95/max read directly.
- Encoded compactly into the CRD `histogram` field on checkpoint.

This is the elegant core: it captures "spikes every Tuesday" that a snapshot misses, at constant
cost.

## 7. Storage decision (the crux)

| Option | Zero-dep? | Restart-safe? | etcd cost | Verdict |
|---|---|---|---|---|
| In-memory only | ✅ | ❌ (cold start every restart) | none | insufficient alone |
| PVC + bbolt/SQLite | ❌ (needs storage) | ✅ | none | breaks "zero infra" |
| **In-memory + CRD checkpoint** | ✅ | ✅ | moderate | **chosen (VPA's hybrid)** |

**Chosen: in-memory live state + periodic CRD checkpoint.** Keeps the zero-infra promise, is
restart-safe, and is debuggable. Cost to budget: ~1 CRD object/workload updated every
`checkpointInterval`. Mitigations: batch writes, only checkpoint changed profiles, configurable
interval (default 5m), backoff under apiserver pressure. At ~40 workloads this is trivial; a
known scaling item documents behavior at thousands of workloads.

## 8. `scan` integration

- New `usage.Provider` (`internal/usage/operator.go`): reads `UsageProfile` CRDs, fills
  `UsageStats`. **New tier constant `TierOperator` (Tier 0.5)**, ordered between `TierSnapshot`
  and `TierHistorical`.
- **Auto-detection** (mirrors Prometheus auto-detect): `selectUsageProvider` order becomes
  `--prometheus-url` → Prometheus auto-detect → **operator CRDs present** → metrics-server snapshot.
- **Cold-start honesty**: while `window` is short, the report shows
  `data: 0.5 (kubetidy operator, 3h — confidence ramping)` and the rightsizer keeps a (smaller
  than snapshot) safety buffer that shrinks as the window matures. Never pretend thin data is rich.

## 9. Operator internals

- **Build**: kubebuilder/controller-runtime; new `cmd/kubetidy-operator` (third binary, alongside
  `kubetidy` and `kubectl-tidy`).
- **Loop**: a timed reconcile (not event-driven on pods) every `scrapeInterval` (default 30s):
  list `PodMetrics`, attribute to workloads (reuse `internal/kube` discovery + selectors),
  update histograms; checkpoint to CRD every `checkpointInterval`.
- **Safety**: leader election (HA-safe), read-only RBAC on workloads + metrics + pods, write RBAC
  only on `UsageProfile`. Crashloop-safe; best-effort; never blocks or pages.
- **Footprint budget**: requests ~50m CPU / 64Mi memory; must itself pass a kubetidy scan cleanly.

## 10. Footprint, RBAC, failure modes

- **RBAC**: `get/list/watch` on deployments/statefulsets/daemonsets, pods, `pods.metrics.k8s.io`;
  `create/update/patch/get/list` on `usageprofiles.kubetidy.io`; leader-election lease.
- **Failure modes**: metrics-server down → operator idles, keeps prior history, marks staleness;
  apiserver pressure → backoff + longer checkpoint interval; operator absent → `scan` degrades to
  Tier 0 snapshot (no regression).

## 11. Rollout / packaging

- `make operator-deploy` (kind) and a manifest under `hack/operator/` + `config/crd/`.
- Helm chart later. CRD installed via manifest (v1alpha1; versioned for forward-compat).
- `kubectl tidy scan` keeps working with **no operator** (Tier 0), **better with operator**
  (Tier 0.5), **best with Prometheus** (Tier 1).

## 12. Test plan (≥80%)

- **Pure**: histogram add/decay/percentile math; checkpoint encode/decode round-trip — table tests.
- **Operator**: reconcile against a fake clientset + fake metrics; assert CRD status written,
  rehydrate-on-restart, staleness handling — envtest or fakes.
- **Provider**: `operator` UsageProvider reads fake `UsageProfile` CRDs → correct `UsageStats`;
  auto-detect ordering; cold-start tier/labeling.

## 13. Phasing

1. **0.5a** — CRD + histogram core + operator collect/checkpoint + `operator` provider + auto-detect.
2. **0.5b** — cold-start UX, printer columns, `make operator-deploy`, docs.
3. **Phase 3 build-out** (separate specs): `Recommendation` CRD, score-over-time, idle/zombie
   detectors fed by the same history, multi-cluster, guarded apply.

## 14. Open questions

1. **Naming/positioning** — `kubetidy-operator` (humble, read-only) vs framing as "the operator"
   from day one in marketing?
2. **CRD GC** — ownerReference to the workload (auto-clean) vs operator-managed lifecycle?
3. **Per-container vs per-workload object** — one `UsageProfile` per workload (chosen) vs per
   container (more objects, finer GC). Workload-level keeps object count = workload count.
4. **Decay half-life & window policy** — fixed 24h half-life, or configurable per workload via
   an annotation?
5. **Interaction with VPA** — if VPA checkpoints already exist, read them as a bootstrap instead
   of cold-starting our own history?
