// Cloudflare Pages Function handling POST /feedback.
//
// This is the entire backend for the docs feedback widget: it forwards each
// anonymous submission to a Discord channel via an incoming webhook. There is
// no server to run and no storage to manage — Discord holds the messages.
//
// Submissions are gated by a Cloudflare Turnstile challenge: the client sends
// a one-time token that this function verifies server-side before forwarding,
// so a bare script (no token) can't spam the channel.
//
// Configure two encrypted secrets (not plaintext): DISCORD_WEBHOOK_URL and
// TURNSTILE_SECRET. Nothing identifying is forwarded — only the page, the
// quoted excerpt and the reader's message. (The visitor IP is sent to
// Cloudflare's verify API only, as Turnstile recommends; it is not stored or
// forwarded to Discord.)
//
// Local dev: `npx wrangler pages dev docs` with a docs/.dev.vars file
// containing DISCORD_WEBHOOK_URL=... and TURNSTILE_SECRET=... (Cloudflare's
// always-passes test secret 1x0000000000000000000000000000000AA works).

export async function onRequestPost({ request, env }) {
  if (!env.DISCORD_WEBHOOK_URL) {
    return new Response("feedback is not configured", { status: 503 });
  }

  let sub;
  try {
    sub = await request.json();
  } catch {
    return new Response("invalid request", { status: 400 });
  }

  const message = (sub.message || "").trim();
  if (!message) {
    return new Response("message is required", { status: 400 });
  }

  // Verify the Turnstile token before doing anything else. Enforced whenever a
  // secret is configured; skipped only if TURNSTILE_SECRET is unset (e.g. a
  // dev environment without it), never silently in production.
  if (env.TURNSTILE_SECRET) {
    const token = typeof sub.turnstileToken === "string" ? sub.turnstileToken : "";
    if (!token) {
      return new Response("verification required", { status: 403 });
    }
    let ok;
    try {
      ok = await verifyTurnstile(
        env.TURNSTILE_SECRET,
        token,
        request.headers.get("CF-Connecting-IP"),
      );
    } catch {
      return new Response("could not verify request", { status: 502 });
    }
    if (!ok) {
      return new Response("verification failed", { status: 403 });
    }
  }

  const page = oneLine(sub.page);
  const excerpt = oneLine(sub.excerpt);

  const payload = {
    username: "docs feedback",
    // Never let a submission ping the channel.
    allowed_mentions: { parse: [] },
    embeds: [
      {
        title: "New docs feedback",
        description: truncate(message, 4000),
        fields: [
          { name: "page", value: code(page), inline: true },
          { name: "excerpt", value: truncate(excerpt, 1024) || "—" },
        ],
      },
    ],
  };

  let res;
  try {
    res = await fetch(env.DISCORD_WEBHOOK_URL, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(payload),
    });
  } catch {
    return new Response("could not deliver feedback", { status: 502 });
  }
  if (!res.ok) {
    return new Response("could not deliver feedback", { status: 502 });
  }
  return new Response(null, { status: 204 });
}

// verifyTurnstile checks a Turnstile token with Cloudflare's siteverify API.
async function verifyTurnstile(secret, token, ip) {
  const form = new URLSearchParams();
  form.append("secret", secret);
  form.append("response", token);
  if (ip) form.append("remoteip", ip);

  const res = await fetch(
    "https://challenges.cloudflare.com/turnstile/v0/siteverify",
    { method: "POST", body: form },
  );
  if (!res.ok) {
    throw new Error("siteverify HTTP " + res.status);
  }
  const data = await res.json();
  return data.success === true;
}

// oneLine collapses whitespace so a value stays on a single line.
function oneLine(s) {
  return (s || "").replace(/\s+/g, " ").trim();
}

function truncate(s, n) {
  return s.length <= n ? s : s.slice(0, n - 1) + "…";
}

// code wraps a value in an inline code span (Discord rejects empty values).
function code(s) {
  return s ? "`" + s + "`" : "—";
}
