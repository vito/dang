// Cloudflare Pages Function handling POST /feedback.
//
// This is the entire backend for the docs feedback widget: it forwards each
// anonymous submission to a Discord channel via an incoming webhook. There is
// no server to run and no storage to manage — Discord holds the messages.
//
// Configure the webhook URL as the DISCORD_WEBHOOK_URL environment variable
// (set it as an encrypted secret, not plaintext). Nothing identifying is
// forwarded: only the page, the quoted excerpt and the reader's message.
//
// Local dev: `npx wrangler pages dev docs` with a docs/.dev.vars file
// containing DISCORD_WEBHOOK_URL=...

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
