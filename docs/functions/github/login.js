// Cloudflare Pages Function handling GET /github/login.
//
// Step one of the GitHub OAuth web flow for the GraphQL playground: redirect
// the visitor to GitHub's authorize screen. The matching callback
// (functions/github/callback.js) completes the exchange.
//
// Why a server hop at all, when we want everything client-side? Only the
// code↔token exchange needs the OAuth client secret, which a static site
// can't hold. These two tiny functions own the secret; the resulting token is
// handed to the browser, which then talks to api.github.com directly (GitHub's
// GraphQL endpoint permits CORS), so introspection and queries stay
// client-side as intended.
//
// Configure secrets (encrypted, not plaintext):
//   GITHUB_CLIENT_ID      — the OAuth app's client ID
//   GITHUB_CLIENT_SECRET  — the OAuth app's client secret (used by callback.js)
// Optional:
//   GITHUB_OAUTH_SCOPE    — space-separated scopes (default "read:user")
//
// The OAuth app's "Authorization callback URL" must be <site>/github/callback.
//
// Local dev: `npx wrangler pages dev docs` with a docs/.dev.vars file holding
// GITHUB_CLIENT_ID / GITHUB_CLIENT_SECRET (register a throwaway OAuth app whose
// callback URL is http://localhost:8788/github/callback).

const STATE_COOKIE = "gh_oauth";

export async function onRequestGet({ request, env }) {
  const missing = [];
  if (!env.GITHUB_CLIENT_ID) missing.push("GITHUB_CLIENT_ID");
  if (!env.GITHUB_CLIENT_SECRET) missing.push("GITHUB_CLIENT_SECRET");
  if (missing.length) {
    // Name the unset vars so misconfiguration is obvious. Remember Cloudflare
    // scopes vars per environment: a branch deploy reads the Preview set, not
    // Production.
    return new Response(
      "github auth is not configured; missing in this environment: " +
        missing.join(", "),
      { status: 503 },
    );
  }

  const url = new URL(request.url);
  // Only same-site relative paths are honored, so the flow can't be abused as
  // an open redirector. Anything else falls back to the site root.
  let returnTo = url.searchParams.get("return") || "/";
  if (!returnTo.startsWith("/") || returnTo.startsWith("//")) {
    returnTo = "/";
  }

  // CSRF token: a random value echoed by GitHub and re-checked in the callback.
  // The return path rides along in the same cookie so the callback knows where
  // to send the visitor back.
  const state = randomState();
  const cookieValue = state + "." + b64urlEncode(returnTo);

  const authorize = new URL("https://github.com/login/oauth/authorize");
  authorize.searchParams.set("client_id", env.GITHUB_CLIENT_ID);
  authorize.searchParams.set("redirect_uri", url.origin + "/github/callback");
  authorize.searchParams.set("scope", env.GITHUB_OAUTH_SCOPE || "read:user");
  authorize.searchParams.set("state", state);

  return new Response(null, {
    status: 302,
    headers: {
      Location: authorize.toString(),
      // Lax so the cookie rides the top-level GET redirect back from GitHub.
      "Set-Cookie":
        STATE_COOKIE +
        "=" +
        cookieValue +
        "; Path=/; Max-Age=600; HttpOnly; Secure; SameSite=Lax",
    },
  });
}

function randomState() {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return b64urlEncode(String.fromCharCode.apply(null, bytes));
}

function b64urlEncode(s) {
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}
