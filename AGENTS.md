# Agent Instructions

## Build and Test Environment

Use Docker for all Go formatting, builds, and tests in this repository. Do not rely on a host-installed `go` or `gofmt`; the expected toolchain is the `golang:1.24` Docker image.

Run commands from the repository root:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w <go files>
```

For focused package tests:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/inbound ./internal/agent ./internal/desktop ./internal/gateway ./internal/webhook
```

For full verification before claiming work is complete:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./...
```

For building the CLI:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go build -ldflags "-s -w" -o ./lark ./cmd/lark
```

If version metadata is needed, prefer running the Makefile inside Docker so it computes `VERSION`, `COMMIT`, and `DATE` consistently:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 make build
```

## Workflow Expectations

- Use `gofmt` in Docker after editing Go files.
- Run focused tests for the packages touched by a change before running the full suite.
- Run `go test ./...` in Docker before reporting that code changes are complete.
- If Docker is unavailable, report verification as blocked instead of using host Go as a fallback.
