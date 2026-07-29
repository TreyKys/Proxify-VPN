BIN := bin/edge-agent
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test clean deploy

# Static build: edge boxes run whatever libc their provider's image ships, and a
# binary that refuses to start on one of them is a box that silently has no VPN.
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-trimpath -ldflags "-s -w -X main.version=$(VERSION)" \
		-o $(BIN) ./cmd/edge-agent
	@echo "built $(BIN) ($(VERSION))"

test:
	go test ./...

clean:
	rm -rf bin

# Ship a new agent to a box. Restarting the agent does not touch the WireGuard
# interface, so users stay connected across an agent deploy.
deploy: build
	@test -n "$(HOST)" || (echo "usage: make deploy HOST=root@de-fsn-1.proxify.ng" && exit 1)
	scp $(BIN) $(HOST):/usr/local/bin/edge-agent.new
	ssh $(HOST) 'mv /usr/local/bin/edge-agent.new /usr/local/bin/edge-agent && systemctl restart proxify-edge-agent && systemctl is-active proxify-edge-agent'
