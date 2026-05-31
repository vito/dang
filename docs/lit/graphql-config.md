# GraphQL Configuration

Dang now supports connecting to any GraphQL API, not just Dagger. This makes it possible to use Dang with custom GraphQL APIs while maintaining the same type-safe, functional programming experience.

## Configuration

Dang uses environment variables to configure the GraphQL connection:

### Environment Variables

- `DANG_GRAPHQL_ENDPOINT` - The GraphQL endpoint URL (e.g., `https://api.example.com/graphql`)
- `DANG_GRAPHQL_AUTHORIZATION` - Authorization header value (e.g., `Bearer token123`)
- `DANG_GRAPHQL_HEADER_<NAME>` - Additional HTTP headers (e.g., `DANG_GRAPHQL_HEADER_X_API_KEY=secret`)

### Default Behavior

If no configuration is provided, Dang will connect to Dagger as before, maintaining backward compatibility.

## Examples

### Using GitHub GraphQL API

```bash
export DANG_GRAPHQL_ENDPOINT="https://api.github.com/graphql"
export DANG_GRAPHQL_AUTHORIZATION="Bearer ghp_your_token_here"
dang my-script.dang
```

### Using Custom API with API Key

```bash
export DANG_GRAPHQL_ENDPOINT="https://api.example.com/graphql"
export DANG_GRAPHQL_HEADER_X_API_KEY="your-api-key"
export DANG_GRAPHQL_HEADER_X_CLIENT_ID="your-client-id"
dang my-script.dang
```

### Using with Authorization Header

```bash
export DANG_GRAPHQL_ENDPOINT="https://api.example.com/graphql"
export DANG_GRAPHQL_AUTHORIZATION="Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
dang my-script.dang
```

## How It Works

1. **Schema Introspection**: Dang performs GraphQL introspection on the configured endpoint to discover available types and functions
2. **Type System**: The introspected schema is used to provide type-safe access to your GraphQL API
3. **Function Mapping**: GraphQL queries and mutations become callable functions in Dang
4. **Authorization**: Headers are automatically added to all GraphQL requests

## Header Name Mapping

Environment variable names are converted to HTTP header names by:
- Removing the `DANG_GRAPHQL_HEADER_` prefix
- Converting underscores to hyphens
- Preserving case

Examples:
- `DANG_GRAPHQL_HEADER_X_API_KEY` → `X-API-KEY`
- `DANG_GRAPHQL_HEADER_CONTENT_TYPE` → `CONTENT-TYPE`

## Backward Compatibility

This feature is fully backward compatible. Existing Dang scripts that rely on Dagger will continue to work without any changes.

## Security

- Never commit API keys or tokens to version control
- Use environment variables or secure secret management systems
- Consider using short-lived tokens when possible
- Be careful when sharing environment configurations

## Troubleshooting

### Common Issues

1. **Authentication Errors**: Check that your authorization header is correct and the token is valid
2. **Schema Introspection Fails**: Ensure the endpoint supports GraphQL introspection
3. **Network Errors**: Verify the endpoint URL is correct and accessible

### Debug Mode

Enable debug mode to see detailed information about GraphQL requests:

```bash
dang --debug my-script.dang
```

This will show:
- The GraphQL endpoint being used
- Schema introspection results
- Request/response details
