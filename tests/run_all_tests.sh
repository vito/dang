#!/bin/bash

# Run all Dang test files in the tests directory

set -e -u

echo "Running Dang Test Suite..."
echo "========================="

# Change to the parent directory (where the dang binary is)
cd "$(dirname "$0")/.."

# Make sure we always run with a fresh build
./hack/generate

# run the actual tests
if [ -n "${DAGGER_SESSION_TOKEN:-}" ]; then
  # Already in a Dagger session, run tests directly
  go test ./tests/ -v
else
  # Not in a Dagger session, wrap with dagger run
  dagger run go test ./tests/ -v
fi
