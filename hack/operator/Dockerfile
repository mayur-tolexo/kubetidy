# Multi-stage, cross-compiling build for the kubetidy operator: a tiny static Linux binary on
# a distroless base. The image is ALWAYS Linux (it runs in kind and in Kubernetes, never on
# the developer's host OS).
#
# The build stage runs on the builder's native platform (BUILDPLATFORM) and cross-compiles to
# the requested target (TARGETARCH) via Go's GOOS/GOARCH, so `docker buildx --platform
# linux/amd64,linux/arm64` produces a multi-arch image without QEMU emulation of the compiler.
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETARCH
WORKDIR /src
ENV GOFLAGS=-mod=mod CGO_ENABLED=0 GOOS=linux
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN GOARCH=$TARGETARCH go build -ldflags "-s -w" -o /out/kubetidy-operator ./cmd/kubetidy-operator

FROM gcr.io/distroless/static:nonroot
USER 65532:65532
COPY --from=build /out/kubetidy-operator /usr/local/bin/kubetidy-operator
ENTRYPOINT ["/usr/local/bin/kubetidy-operator"]
