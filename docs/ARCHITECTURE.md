# kubetidy Architecture

This document gives a high-level design (HLD) of kubetidy and the runtime flow of a scan. It
is meant to get a new contributor productive quickly. For the product rationale and roadmap,
see [ROADMAP.md](../ROADMAP.md).

## Design goals

1. **Read-only and reversible by default.** kubetidy never mutates the cluster. `diff` only
   *prints* the `kubectl patch` you would run, and `pr` only *writes files* for you to review
   and merge — kubetidy never commits, pushes, or applies.
2. **Never fail hard.** A missing data source degrades the result; it does not abort the scan.
3. **Every number shows its work.** Confidence and evidence are derived and reproducible.
4. **Action-ready core.** The `Recommendation` type carries the patch that *would* be applied,
   so future action features (GitOps PRs, guarded apply) are new consumers, not rewrites.
5. **Bounded, testable packages.** Pure algorithm packages have no I/O and are table-tested;
   I/O packages sit behind interfaces and are tested with fakes.

## High-Level Design (HLD)

```mermaid
flowchart TB
    user([User: kubectl tidy scan / diff])

    subgraph CLI["cmd/kubetidy + internal/cli"]
        root["root cmd<br/>(adapts name from os.Args[0])"]
        scancmd["scan command"]
        diffcmd["diff command"]
        prcmd["pr command"]
        enginewire["runEngine (shared wiring)"]
    end

    subgraph Core["Scan engine — internal/scan"]
        engine["Engine.Run"]
    end

    subgraph IO["I/O packages (behind interfaces)"]
        kube["internal/kube<br/>client-go: Load + Discover"]
        usage["internal/usage<br/>UsageProvider"]
        pricing["internal/pricing<br/>PriceProvider"]
    end

    subgraph Pure["Pure algorithm packages (no I/O)"]
        rightsizer["internal/rightsizer<br/>Recommend + Confidence"]
        costmodel["internal/costmodel<br/>MonthlySavings"]
        score["internal/score<br/>Efficiency Score"]
        patch["internal/patch<br/>StrategicMergePatch"]
        gitops["internal/gitops<br/>Build (patches + PR body)"]
    end

    subgraph Out["Rendering — internal/report"]
        report["Table / JSON / Explain"]
    end

    model["internal/model<br/>(domain types: Workload,<br/>Recommendation, ScanResult)"]

    cluster[("Kubernetes API<br/>metrics-server<br/>Prometheus")]

    user --> root --> scancmd & diffcmd & prcmd --> enginewire --> engine
    engine --> kube --> cluster
    engine --> usage --> cluster
    engine --> pricing
    engine --> rightsizer & costmodel & score
    scancmd --> report
    diffcmd --> patch
    prcmd --> gitops --> patch
    Core -.uses.-> model
    IO -.uses.-> model
    Pure -.uses.-> model
    Out -.uses.-> model
```

The dependency rule is one-directional: everything depends on `internal/model`; nothing in
`model` depends on anything else. Pure packages never import I/O packages.

## The three-tier data ladder

kubetidy auto-selects the best data source available and stamps every finding with the tier
that proved it.

```mermaid
flowchart LR
    start([scan starts]) --> q0{--prometheus-url<br/>given?}
    q0 -- yes --> t1["Tier 1: Prometheus<br/>P50/P95/max over window<br/>HIGH confidence"]
    q0 -- no --> qa{auto-detect<br/>in-cluster Prometheus?}
    qa -- found --> t1
    qa -- none --> q2{metrics-server<br/>available?}
    q2 -- yes --> t0["Tier 0: metrics-server<br/>live snapshot<br/>LOW–MED confidence"]
    q2 -- no --> ts["Static: spec-only checks<br/>(missing/absurd requests)<br/>LOWEST confidence"]
    t1 --> rec([recommendations])
    t0 --> rec
    ts --> rec
```

Tier 2 (OpenCost, for precise allocated cost) is defined behind the `PriceProvider` interface
and deferred — see the roadmap.

## Scan flow (sequence)

```mermaid
sequenceDiagram
    actor U as User
    participant CLI as cli (scan/diff)
    participant K as kube
    participant E as scan.Engine
    participant UP as UsageProvider
    participant PP as PriceProvider
    participant RS as rightsizer
    participant CM as costmodel
    participant SC as score
    participant R as report/patch

    U->>CLI: kubectl tidy scan -n ns
    CLI->>K: Load(context) + Discover(ns)
    K-->>CLI: []Workload (normalized to millicores/bytes)
    CLI->>E: Engine.Run(ctx)
    loop per workload
        E->>UP: Usage(workload)
        UP-->>E: per-container UsageStats (or error → warning)
        E->>PP: ResourcePrice(workload)
        PP-->>E: $/core-month, $/GiB-month
        loop per container
            E->>RS: Recommend(current, usage, policy)
            RS-->>E: proposed requests/limits
            E->>RS: Confidence(usage)
            RS-->>E: confidence + reason
            E->>CM: MonthlySavings(current, proposed, price, replicas)
            CM-->>E: $/month delta
        end
    end
    E->>SC: Compute(result)
    SC-->>E: efficiency score + breakdown
    E-->>CLI: ScanResult
    CLI->>R: render (Table/JSON) or KubectlCommand (diff)
    R-->>U: report / reversible patches
```

## Packages at a glance

| Package | Kind | Responsibility |
|---------|------|----------------|
| `internal/model` | types | Domain types shared by all packages; no dependencies |
| `internal/kube` | I/O | kubeconfig loading + workload discovery (client-go) |
| `internal/usage` | I/O | `UsageProvider`: metrics-server (Tier 0), Prometheus (Tier 1) |
| `internal/pricing` | I/O | `PriceProvider`: blended config defaults, instance-type refinement |
| `internal/rightsizer` | pure | usage + policy → recommended resources; confidence model |
| `internal/costmodel` | pure | resource delta + price → $/month |
| `internal/score` | pure | scan result → 0–100 efficiency score + breakdown |
| `internal/patch` | pure | recommendation → strategic-merge patch + `kubectl patch` command |
| `internal/gitops` | pure | scan result → GitOps change set (patch files + Markdown PR body) |
| `internal/scan` | orchestrator | wires providers + pure packages into a `ScanResult` |
| `internal/report` | output | Table / JSON / `--explain` rendering |
| `internal/cli` | entrypoint | cobra commands (`scan`/`diff`/`pr`); shared `runEngine` with an injectable client-loader seam |
| `internal/version` | meta | build/version metadata (ldflags) |

`internal/usage` also contains `DetectPrometheus`, which probes well-known in-cluster
Prometheus service names so a scan auto-upgrades from Tier 0 to Tier 1 with no configuration.

## Extending kubetidy

- **New data source** (e.g. OpenCost, a managed metrics service): implement `UsageProvider`
  or `PriceProvider` and select it in `internal/cli`. Nothing else changes.
- **New output format**: add a renderer in `internal/report` and a `--output` case.
- **New action** (GitOps PR, guarded apply): consume the existing `Recommendation` /
  `internal/patch` output — the core stays read-only.
