\use-plugin{dang}

# GraphQL + Dang playground {#github-playground}

> Meta: the live payoff of [#graphql] — "schema-as-stdlib" against a real API the
> reader already has an account on. Keep it concrete: sign in, run, see your own
> data. Don't re-teach selection syntax (that's [#graphql]); show it working.
> Honesty: the only thing not client-side is the OAuth token exchange, and the
> token stays in the tab. Say that plainly, don't oversell.

Every other playground in these docs runs the standard library alone. This one
wires in a *live* GraphQL schema: sign in with GitHub and `import GitHub` brings
GitHub's real types and root fields into scope, queried straight from your
browser.

## Sign in, then run

Hit **Sign in with GitHub** in the toolbar below and authorize the app. You come
back to this page signed in; now run the snippet.

\dang-github-playground{{{
import GitHub

# `viewer` is GitHub's root field for the authenticated user. The selection
# desugars to one GraphQL query — see the GraphQL interop page.
viewer.{
  login
  name
  repositories(first: 3).{ nodes.{ name, stargazerCount } }
}
}}}

- `import GitHub` introspects GitHub's schema the first time you run (a few
  seconds — it's a big schema), then caches it for the rest of the session
- root `Query` fields like `viewer` and `repository` become callable functions
- `.{ ... }` selections, nested fields, and arguments all work as in [#graphql]

## What's actually happening

- introspection and every query are ordinary requests from your browser to
  `api.github.com` — GitHub's GraphQL endpoint allows cross-origin calls, so no
  proxy sits in between
- the only server step is the OAuth code-for-token exchange, which needs a
  client secret a static site can't hold; a small function does just that and
  hands the token back
- that token lives only in this tab's `sessionStorage` — it's never stored
  server-side and is forgotten when you close the tab. **Sign out of GitHub**
  clears it immediately
- the sign-in requests read-only access (`read:user`); you're querying your own
  account, so only you see the results
