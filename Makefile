.PHONY: test test-unit test-integration test-sdk test-all bench build clean

# Build
build:
	go build -o gateway ./cmd/gateway
	go build -o proxy ./cmd/proxy

# Unit tests (fast, no external deps)
test-unit:
	go test ./api/... ./internal/... -count=1

# Integration tests (embedded NATS, mock upstream)
test-integration:
	go test ./test/integration/... -count=1

# JS SDK tests (requires NATS on localhost:4222 for client tests)
test-sdk:
	cd sdk/js && npx vitest run

# All Go tests
test: test-unit test-integration

# Everything
test-all: test test-sdk

# Benchmarks
bench:
	go test ./test/benchmark/... -bench=. -benchmem -count=1

# Verbose test output
test-v:
	go test ./api/... ./internal/... ./test/integration/... -v -count=1

clean:
	rm -f gateway proxy
