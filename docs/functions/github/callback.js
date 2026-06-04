// Cloudflare Pages Function handling GET /github/callback.
//
// Step two of the GitHub OAuth web flow (see functions/github/login.js): GitHub
// redirects here with ?code & ?state. We verify the state against the cookie
// login.js set, exchange the code for an access token using the client secret,
// then bounce the visitor back to the page they started from with the token in
// the URL fragment (#gh_token=…). The fragment is never sent to a server, and
// playground.js immediately stashes it in sessionStorage and strips it from
// the URL.
//
// Secrets (encrypted): GITHUB_CLIENT_ID, GITHUB_CLIENT_SECRET. See login.js.

const STATE_COOKIE = "gh_oauth";

// Clears the state cookie; sent on every response so it never lingers.
const CLEAR_COOKIE =
  STATE_COOKIE + "=; Path=/; Max-Age=0; HttpOnly; Secure; SameSite=Lax";

export async function onRequestGet({ request, env }) {
  const missing = [];
  if (!env.GITHUB_CLIENT_ID) missing.push("GITHUB_CLIENT_ID");
  if (!env.GITHUB_CLIENT_SECRET) missing.push("GITHUB_CLIENT_SECRET");
  if (missing.length) {
    return fail("github auth is not configured; missing in this environment: " + missing.join(", "), 503);
  }

  const url = new URL(request.url);
  const code = url.searchParams.get("code");
  const state = url.searchParams.get("state");
  if (!code || !state) {
    return fail("missing code or state", 400);
  }

  // Verify CSRF state and recover the return path from the cookie login.js set.
  const cookie = parseCookie(request.headers.get("Cookie") || "")[STATE_COOKIE];
  if (!cookie) {
    return fail("missing or expired login state", 400);
  }
  const dot = cookie.indexOf(".");
  const cookieState = dot === -1 ? cookie : cookie.slice(0, dot);
  if (!timingSafeEqual(cookieState, state)) {
    return fail("state mismatch", 400);
  }
  let returnTo = dot === -1 ? "/" : b64urlDecode(cookie.slice(dot + 1));
  if (!returnTo.startsWith("/") || returnTo.startsWith("//")) {
    returnTo = "/";
  }

  // Exchange the code for an access token.
  let token;
  try {
    const res = await fetch("https://github.com/login/oauth/access_token", {
      method: "POST",
      headers: { Accept: "application/json", "content-type": "application/json" },
      body: JSON.stringify({
        client_id: env.GITHUB_CLIENT_ID,
        client_secret: env.GITHUB_CLIENT_SECRET,
        code: code,
        redirect_uri: url.origin + "/github/callback",
      }),
    });
    const data = await res.json();
    if (data.error || !data.access_token) {
      return fail("token exchange failed: " + (data.error_description || data.error || "no token"), 502);
    }
    token = data.access_token;
  } catch (e) {
    return fail("could not reach GitHub to exchange the code", 502);
  }

  // Hand the token back via the fragment and clear the state cookie.
  return new Response(null, {
    status: 302,
    headers: {
      Location: url.origin + returnTo + "#gh_token=" + encodeURIComponent(token),
      "Set-Cookie": CLEAR_COOKIE,
    },
  });
}

// fail returns an error response that also clears the state cookie.
function fail(message, status) {
  return new Response(message, {
    status: status,
    headers: { "Set-Cookie": CLEAR_COOKIE },
  });
}

function parseCookie(header) {
  const out = {};
  header.split(";").forEach(function (part) {
    const i = part.indexOf("=");
    if (i === -1) return;
    out[part.slice(0, i).trim()] = part.slice(i + 1).trim();
  });
  return out;
}

// timingSafeEqual compares two strings without short-circuiting on length/char.
function timingSafeEqual(a, b) {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}

function b64urlDecode(s) {
  s = s.replace(/-/g, "+").replace(/_/g, "/");
  while (s.length % 4) s += "=";
  try {
    return atob(s);
  } catch (e) {
    return "/";
  }
}
