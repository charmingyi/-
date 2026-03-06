#!/usr/bin/env bash
set -euo pipefail

mkdir -p releases

echo "Building panel..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o releases/panel-linux-amd64 ./cmd/panel

echo "Building agent..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o releases/agent-linux-amd64 ./cmd/agent
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o releases/agent-linux-arm64 ./cmd/agent
GOOS=linux GOARCH=arm CGO_ENABLED=0 GOARM=7 go build -o releases/agent-linux-arm ./cmd/agent

echo "Done. files in releases/"

