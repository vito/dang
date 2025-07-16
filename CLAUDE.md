Read ./llm-notes/ to refresh your memory.

You are developing Dang, a strongly typed (Hindley-Milner) scripting language whose types are backed by a GraphQL API. Its grammar is defined in pkg/dang/dang.peg.

NEVER GUESS DANG CODE SYNTAX. You do NOT know this language already. Study other Dang code, and the Dang grammar (pkg/dang/dang.peg) BEFORE attempting to write any Dang code.

Run ./tests/run_all_tests.sh to validate your changes. This is an all-in-one script that re-generates all generated code, re-builds the Dang binary, and runs all of the tests.

Run ./hack/generate to re-generate all generated code without building or running the tests. DO NOT run generation commands yourself; trust the script.

To run an individual test, use go test ./tests/ -v -run /test_name, but remember to re-generate when needed. Also, check the output to make sure something actually ran.

Avoid changing your working directory unless the task expressly needs it. The hack/ scripts should suffice in general.

ALWAYS start by adding a test if one is missing, and work towards making the test pass.

Don't look for APIs - you won't find them in this codebase, because they're all derived from the Dagger GraphQL schema.

## Taking Notes

Take notes of important language design or implementation details as you learn them, beneath ./llm-notes/, with a name that will help your future self.

Maintain the accuracy of ./llm-notes/ as the project evolves.

- Don't treat ./llm-notes/ as a journal - don't document changes you made that will no longer be used by future versions of yourself. Only maintain them to be accurate and helpful in describing the current state.

## Dagger SDK

To test the Dagger SDK, do not use `go test`. Instead, call `dagger functions` as a quick spot check - you should see 'build' and 'test' functions. Use `dagger call test` to run them.

Run all `dagger` commands from the root of the repo, NOT the dagger-sdk directory.

## Code Organization

- When adding code to the GraphQL test server, never add helper functions to resolvers.go - always add them to resolvers.helpers.go.