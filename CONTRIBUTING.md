# Contributing to kubetidy

Thanks for your interest! kubetidy is open stack, Kubernetes-native, and built to be
contributed to. This guide gets you productive fast.

## Project shape

kubetidy is a single Go binary with clean, independently testable packages:

```
cmd/kubetidy           main(); cobra commands
internal/model         domain types (no dependencies)
internal/kube          client-go setup + workload discovery
internal/usage         UsageProvider: metricsserver, prometheus
internal/pricing       PriceProvider: config/default pricing
internal/rightsizer    PURE: usage + policy -> recommendation
internal/costmodel     PURE: resources + pricing -> $/month
internal/score         PURE: scan result -> efficiency score
internal/report        renderers (table, json, explain)
internal/scan          orchestrator wiring everything together
```

The three core algorithm packages — `rightsizer`, `costmodel`, `score` — are **pure
functions** with no I/O. They are the easiest and highest-value place to contribute, and
they're covered by table-driven tests.

## Dev setup

Requires **Go 1.26+**. This repo lives under `$GOPATH/src` and the environment sets
`GOFLAGS=-mod=vendor` globally; the `Makefile` overrides with `-mod=mod` so you don't have
to think about it.

```sh
make deps     # download/tidy modules
make build    # build ./bin/kubetidy and ./bin/kubectl-tidy
make test     # run all tests
make lint     # gofmt + go vet + golangci-lint (if installed)
make check    # test + lint (run this before opening a PR)
```

## Ground rules

1. **Read-only and reversible by default.** Never add a code path that mutates cluster state
   without an explicit, opt-in, reversible flow. This is the project's core promise.
2. **Every number shows its work.** Any value a user sees must be reproducible via
   `--explain`. No magic numbers.
3. **TDD.** Write the failing test first, especially for the pure packages.
4. **Keep packages bounded.** New behavior goes behind the existing interfaces
   (`UsageProvider`, `PriceProvider`) where possible.
5. **`make check` must pass.** gofmt-clean, `go vet`-clean, tests green.

## Commit & PR conventions

- Conventional commits: `feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `chore:`.
- One logical change per PR. Include tests. Update docs/roadmap if behavior changes.
- Be kind. See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

## Good first issues

Look for the [`good first issue`](https://github.com/kubetidy/kubetidy/labels/good%20first%20issue)
label. Adding a new instance-type to the pricing table, a new output format, or a test case
for the rightsizer are all great starting points.
