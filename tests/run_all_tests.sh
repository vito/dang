#!/bin/bash

# Run all Dash test files in the tests directory

echo "Running Dash Test Suite..."
echo "========================="

# Change to the parent directory (where the dash binary is)
cd "$(dirname "$0")/.."

# Make sure we always run with a fresh build
go generate -x ./...
go -C treesitter generate -x ./...
go build -o dash ./cmd/dash

failed_tests=0
total_tests=0

# Run each .dash file in the tests directory
for test_file in tests/*.dash; do
    if [ -f "$test_file" ]; then
        echo
        echo "Running $(basename "$test_file")..."
        total_tests=$((total_tests + 1))

        if ./dash "$test_file" > /dev/null 2>&1; then
            echo "✅ PASSED: $(basename "$test_file")"
        else
            echo "❌ FAILED: $(basename "$test_file")"
            echo "   Error output:"
            ./dash "$test_file" 2>&1 | sed 's/^/   /'
            failed_tests=$((failed_tests + 1))
        fi
    fi
done

echo
echo "========================="
echo "Test Results:"
echo "Total tests: $total_tests"
echo "Passed: $((total_tests - failed_tests))"
echo "Failed: $failed_tests"

if [ $failed_tests -eq 0 ]; then
    echo "🎉 All tests passed!"
    exit 0
else
    echo "💥 Some tests failed!"
    exit 1
fi
