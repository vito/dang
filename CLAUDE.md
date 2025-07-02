Read ./llm-notes/ to refresh your memory.

You are developing Dash, a strongly typed (Hindley-Milner) scripting language whose types are backed by a GraphQL API. Its grammar is defined in pkg/dash/dash.peg.

NEVER GUESS DASH CODE SYNTAX. You do NOT know this language already. Study other Dash code, and the Dash grammar (pkg/dash/dash.peg) BEFORE attempting to write any Dash code.

Run ./tests/run_all_tests.sh to validate your changes. This is an all-in-one script that re-generates all generated code, re-builds the Dash binary, and runs all of the tests.

Run ./hack/generate to re-generate all generated code without building or running the tests. DO NOT run generation commands yourself; trust the script.

To run an individual test, use go test ./tests/ -run /test_name, but remember to re-generate when needed.

Avoid changing your working directory unless the task expressly needs it. The hack/ scripts should suffice in general.

ALWAYS start by adding a test if one is missing, and work towards making the test pass.

Don't look for APIs - you won't find them in this codebase, because they're all derived from the Dagger GraphQL schema.

This language favors simplicity over all else. For example, there is no operator precedence - just use parentheses.

Take notes of important language design or implementation details as you learn them, beneath ./llm-notes/, with a name that will help your future self.

Maintain the accuracy of ./llm-notes/ as the project evolves.
