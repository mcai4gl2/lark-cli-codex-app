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

## Documented exception: codex integration test

`internal/slack/codex_integration_test.go` (build tag `integration`) drives the real
host `codex` CLI and its real `~/.codex` auth, neither of which exists inside the
`golang:1.24` container. For this test only, compile the binary *in* Docker (honoring
the toolchain rule) and *run* it on the host (Linux host + Linux container → compatible):

```bash
# 1) compile the integration test binary in Docker (no host go toolchain used)
docker run --rm -v "$PWD:/work" -w /work golang:1.24 \
  go test -c -tags integration -o codex_integration.test ./internal/slack

# 2) run it on the host against the real codex CLI + real auth
CODEX_INTEGRATION_TEST=1 ./codex_integration.test -test.v -test.run TestCodexIntegration

# cleanup
rm -f codex_integration.test
```

It is gated twice (the `integration` build tag and `CODEX_INTEGRATION_TEST=1`), so the
normal `go test ./...` never compiles or runs it. It never mutates `~/.codex` or runs any
login/config command; if the env is not ready the `prerequisite` subtest skips with the
error printed. See `plans/20260623_codex-integration-test.md`.
