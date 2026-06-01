# Design: storage model + extensible CRD family

**Status:** Approved (brainstorm 2026-06-01) — implementing
**Related:** [operator.md](operator.md), [ARCHITECTURE.md](../ARCHITECTURE.md)

## 1. Storage: no time-series DB; configurable decay

Recommendations need the usage *distribution* (P50/P95/max), not time-ordered points. The
operator keeps a fixed-size, exponentially-decaying histogram per container per metric and
checkpoints it into the `UsageProfile` CRD. This stays.

- **No TSDB**, no per-time data points, no 6-month retention ring — those serve graphing, not
  recommendations, and would add heavy dependencies for no recommendation gain.
- The real gap is decay *memory*: the 24h half-life under-remembers weekly peaks. Fix: make
  the half-life **configurable** (`--cpu-half-life` / `--memory-half-life`, default **7d**),
  so a full week of behaviour is retained. Footprint is unchanged at any half-life.
- Out of scope (YAGNI): time series, downsampling, Prometheus/TSDB. A future "graph usage
  over months" goal is a separate spec; the CRD design below leaves room without rework.

## 2. CRD family: typed, versioned, extensible

Three CRDs under one API group `kubetidy.io`, generated with **controller-gen** (the toolchain
Cluster API uses): annotated Go types under `api/v1alpha1/` produce a validated OpenAPI
schema + deepcopy + a typed clientset consumers can import.

| CRD | Writer | Purpose |
|---|---|---|
| `UsageProfile` | operator | per-workload usage history (histogram + P50/P95/max) |
| `ClusterUsageSummary` | operator | per-cluster rollup: usage, total/wasted cost, efficiency score, top-N targets — the per-cluster view dashboards / Cluster API read |
| `Recommendation` | recommender (rules now, **LLM later**) | one scored, rankable recommendation: `$/mo`, confidence, reversible patch, evidence |

**Extensibility, three layers:**
1. **Typed stable core** — `targetRef`, summary percentiles, cost, score: real OpenAPI schema
   with validation + printer columns.
2. **Versioning + compatibility** — `v1alpha1 → v1beta1 → v1`; additive-only within a version;
   documented conversion on bump. This is the contract external products rely on.
3. **Explicit extension area** — a typed-but-open `extensions map[string]string` region for
   experimental/vendor fields, so kubetidy and integrators extend without a schema migration.

**`Recommendation` shape (the LLM target):**
```
spec:   targetRef, source (rules|llm), generatedAt, inputsRef (UsageProfile + window)
status: score (0..100), confidence, monthlySavings, action (patch), evidence[], explanation, model
```
A rules recommender populates these today; **swapping in an LLM is a new writer of the same
CRD — no schema change.** The cluster/UI ranks by `score`.

**Integration surface:** CRDs only, read via the Kubernetes API (Cluster API already watches
CRDs; no new server to run/secure). A metrics/HTTP surface is deferred.

**Migration:** `UsageProfile v1alpha1` currently uses `preserve-unknown-fields`, so adding a
typed schema is backward-compatible. controller-gen lands the schema; read/write can move from
the dynamic client to the typed clientset incrementally.

## 3. Install UX (best possible)

`kubectl tidy init` already installs the CRD(s) + operator from binary-embedded manifests
(server-side apply, waits for Established). Extend it to apply all three CRDs and keep the
single-command, no-`kubectl apply -f` experience. krew + `curl|sh` + a release pipeline
(Phase 1 packaging) make first install one line.

## 4. Implementation increments (verified independently)

1. **Configurable half-life** (default 7d) — small, high-value, no schema change.
2. **controller-gen migration** of `UsageProfile` to typed `api/v1alpha1` + generated CRD,
   backward-compatible; `init` keeps working.
3. **`ClusterUsageSummary`** CRD + operator rollup writer.
4. **`Recommendation`** CRD + a rules recommender writing it (LLM-ready, LLM not built).
5. **Install UX**: `init` applies all CRDs; Phase-1 packaging (krew/curl/release).
