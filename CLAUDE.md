Read ./llm-notes/ to refresh your memory.

You are developing Bind, a strongly typed (Hindley-Milner) scripting language whose types are backed by a GraphQL API. Its grammar is defined in pkg/bind/bind.peg.

NEVER GUESS BIND CODE SYNTAX. You do NOT know this language already. Study other Bind code, and the Bind grammar (pkg/bind/bind.peg) BEFORE attempting to write any Bind code.

Run ./tests/run_all_tests.sh to validate your changes. This is an all-in-one script that re-generates all generated code, re-builds the Bind binary, and runs all of the tests.

Run ./hack/generate to re-generate all generated code without building or running the tests. DO NOT run generation commands yourself; trust the script.

To run an individual test, use go test ./tests/ -v -run /test_name, but remember to re-generate when needed. Also, check the output to make sure something actually ran.

Avoid changing your working directory unless the task expressly needs it. The hack/ scripts should suffice in general.

ALWAYS start by adding a test if one is missing, and work towards making the test pass.

Don't look for APIs - you won't find them in this codebase, because they're all derived from the Dagger GraphQL schema.

This language favors simplicity over all else. For example, there is no operator precedence - just use parentheses.

## Taking Notes

Take notes of important language design or implementation details as you learn them, beneath ./llm-notes/, with a name that will help your future self.

Maintain the accuracy of ./llm-notes/ as the project evolves.

## Dagger SDK

To test the Dagger SDK, do not use `go test`. Instead, call `dagger functions` as a quick spot check - you should see 'build' and 'test' functions. Use `dagger call test` to run them.

Run all `dagger` commands from the root of the repo, NOT the dagger-sdk directory.
