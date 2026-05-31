#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
go run . -i lit/index.md -o . --html-templates html "$@"
