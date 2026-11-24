# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder

ENV GOTOOLCHAIN=auto
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY pkg/ pkg/
COPY agent/ agent/

ARG TARGETOS=linux
ARG TARGETARCH=amd64

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/starmoe-status-agent ./agent/cmd/status-agent

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/starmoe-status-agent /usr/local/bin/starmoe-status-agent

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/starmoe-status-agent"]
