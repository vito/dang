#!/bin/bash

# Run all Sprout test files in the tests directory

set -e -u

echo "Running Sprout Test Suite..."
echo "========================="

# Change to the parent directory (where the sprout binary is)
cd "$(dirname "$0")/.."

# Make sure we always run with a fresh build
./hack/generate

# run the actual tests
dagger run go test ./tests/ -v
