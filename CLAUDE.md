You are developing Dash, a strongly typed (Hindley-Milner) scripting language whose types are backed by a GraphQL API. Its grammar is defined in pkg/dash/dash.peg.
Run ./tests/run_all_tests.sh to validate your changes. This is an all-in-one script that re-generates all generated code, re-builds the Dash binary, and runs all of the tests.
Run ./hack/generate to re-generate all generated code without building or running the tests. DO NOT run generation commands yourself; trust the script.
Try to avoid changing your working directory unless the task expressly needs it. The hack/ scripts should suffice in general.
ALWAYS start by adding a test if one is missing, and work towards making the test pass.
Don't look for APIs - you won't find them in this codebase, because they're all derived from the Dagger GraphQL schema.
This language favors simplicity over all else. For example, there is no operator precedence - just use parentheses.
