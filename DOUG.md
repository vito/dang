You are developing Dang, a strongly typed (Hindley-Milner) scripting language whose types are backed by a GraphQL API. Its grammar is defined in pkg/dang/dang.peg.

NEVER GUESS DANG CODE SYNTAX. You do NOT know this language already. Study other Dang code, and the Dang grammar (pkg/dang/dang.peg) BEFORE attempting to write any Dang code.

## Testing

Tests are created as tests/test_SOME_NAME.dang. ALWAYS start by adding a test if one is missing, and work towards making the test pass.

Use the Test tool to validate your changes. This is an all-in-one tool that re-generates all generated code and runs all of the tests. You can optionally pass a filter to only run a subset of tests.

## Taking Notes

Read ./llm-notes/*.md to refresh your memory. These are notes written by yourself in the past, for future reference. As the language design, foundational mechanics, and principles change, you MUST update these notes to reflect the current state of the project.

DO NOT add useless notes to ./llm-notes/. Only record and maintain notes that a future version of yourself would benefit from reading.

## Code Organization

- Don't look for API implementations backing method calls in Dang code - you won't find them in this codebase, because they're all derived from the Dagger GraphQL schema.
- When adding code to the GraphQL test server, never add helper functions to resolvers.go - always add them to resolvers.helpers.go.
