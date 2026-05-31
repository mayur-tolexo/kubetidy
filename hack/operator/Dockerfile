# Multi-stage build for the kubetidy operator: a tiny static binary on a distroless base.
FROM golang:1.26 AS build
WORKDIR /src
ENV GOFLAGS=-mod=mod CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags "-s -w" -o /out/kubetidy-operator ./cmd/kubetidy-operator

FROM gcr.io/distroless/static:nonroot
USER 65532:65532
COPY --from=build /out/kubetidy-operator /usr/local/bin/kubetidy-operator
ENTRYPOINT ["/usr/local/bin/kubetidy-operator"]
