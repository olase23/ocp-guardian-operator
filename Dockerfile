# Build the manager binary
FROM golang:1.25 AS builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.sum ./
# Cache deps before building and copying source so that we don't need to re-download as
# much and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY main.go main.go
COPY api/ api/
COPY internal/ internal/

# Build
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -a -o manager main.go

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
