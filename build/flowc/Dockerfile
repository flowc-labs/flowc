FROM golang:1.25 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -ldflags "-s -w" -a -o flowc ./cmd/flowc

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/flowc .
USER 65532:65532
ENTRYPOINT ["/flowc"]
