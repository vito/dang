// Anonymous per-element documentation feedback.
//
// On hover, each paragraph, list item, heading and code block grows a small
// "feedback" link. Clicking it opens a message bubble; submitting POSTs the
// page, the quoted text and the reader's message to the /feedback endpoint
// (a Cloudflare Pages Function that forwards to Discord). Nothing identifying
// is collected or sent.
//
// Submission is gated by a Cloudflare Turnstile challenge so bots can't spam
// the endpoint. The Turnstile script is loaded lazily — only when a reader
// first opens a feedback bubble — so browsing the docs is completely
// unaffected.
(function () {
  "use strict";

  var ENDPOINT = "feedback"; // relative to the page; resolves to /feedback

  // Public Turnstile site key for danglang.org. Safe to embed; the matching
  // secret lives only in the Pages Function (TURNSTILE_SECRET).
  var TURNSTILE_SITEKEY = "0x4AAAAAADeX0HJWPHsuYMp1";

  // Lazily load the Turnstile API the first time a bubble opens. Resolves with
  // window.turnstile; cached so the script is fetched at most once.
  var turnstileReady = null;
  function ensureTurnstile() {
    if (turnstileReady) return turnstileReady;
    turnstileReady = new Promise(function (resolve, reject) {
      var s = document.createElement("script");
      s.src = "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";
      s.async = true;
      s.defer = true;
      s.onload = function () {
        resolve(window.turnstile);
      };
      s.onerror = function () {
        turnstileReady = null; // allow a retry on the next open
        reject(new Error("turnstile failed to load"));
      };
      document.head.appendChild(s);
    });
    return turnstileReady;
  }

  // Elements that can be commented on. Code blocks get a corner-pinned link
  // since their content can scroll horizontally.
  var INLINE = "main p, main li, main h1, main h2, main h3, main h4, main h5, main h6";
  var BLOCK = "main pre";

  var hoverRule = (INLINE + ", " + BLOCK)
    .split(", ")
    .map(function (sel) {
      return sel + ":hover > .dang-fb-link > .dang-fb-chip";
    })
    .join(", ");

  var style = document.createElement("style");
  style.textContent = [
    "main p, main li, main h1, main h2, main h3, main h4, main h5, main h6, main pre { position: relative; }",
    // The clickable host is an inline, ZERO-WIDTH box, so the hidden
    // (opacity:0) link adds no advance to the line and can never bump a
    // paragraph's trailing word onto a new line. The visible "feedback" pill
    // lives in a child (.dang-fb-chip) painted just past the host, to the right
    // of the text — staying inline where it belongs instead of floating into
    // the content. main clips horizontal overflow (rule below), so a pill
    // trailing a full last line runs off the column edge rather than forcing a
    // horizontal scrollbar.
    ".dang-fb-link {",
    "  display: inline-block; cursor: pointer; user-select: none;",
    "  margin: 0; padding: 0; border: 0; background: none; font: inherit;",
    "}",
    ".dang-fb-link:not(.dang-fb-link--corner) {",
    "  position: relative; width: 0; vertical-align: baseline;",
    "}",
    ".dang-fb-link:not(.dang-fb-link--corner) > .dang-fb-chip {",
    "  position: absolute; left: .4em; bottom: -.15em;",
    "}",
    ".dang-fb-chip {",
    "  font-size: .72rem; white-space: nowrap; padding: 0 .35em;",
    "  border-radius: 4px; border: 1px solid var(--code-border);",
    "  background: var(--bg3); color: var(--fg2);",
    "  opacity: 0; transition: opacity .12s ease;",
    "}",
    hoverRule + ", .dang-fb-link:focus > .dang-fb-chip { opacity: 1; }",
    ".dang-fb-link:hover > .dang-fb-chip { color: var(--accent2); border-color: var(--accent); }",
    // Code blocks keep the corner-pinned link; its chip just rides along inside
    // the pinned host (no zero-width / overflow trick needed there).
    ".dang-fb-link--corner {",
    "  position: absolute; top: .45rem; right: .5rem; z-index: 2;",
    "}",
    // Clip the feedback pill at the content column's edge so a pill trailing a
    // full last line runs offscreen instead of adding a horizontal scrollbar.
    "main { overflow-x: clip; }",
    ".dang-fb-bubble {",
    "  position: absolute; z-index: 50; width: min(22rem, 90vw);",
    "  background: var(--bg2); border: 1px solid var(--code-border);",
    "  border-radius: 8px; padding: .75rem; box-shadow: 0 8px 28px rgba(0,0,0,.35);",
    "}",
    ".dang-fb-quote {",
    "  font-size: .72rem; color: var(--fg2); margin-bottom: .5rem;",
    "  border-left: 2px solid var(--accent); padding-left: .5rem;",
    "  max-height: 3.2em; overflow: hidden;",
    "}",
    ".dang-fb-bubble textarea {",
    "  width: 100%; min-height: 4.5rem; resize: vertical; font: inherit;",
    "  font-size: .85rem; color: var(--fg); background: var(--code-bg);",
    "  border: 1px solid var(--code-border); border-radius: 6px; padding: .5rem;",
    "}",
    ".dang-fb-row { display: flex; align-items: center; justify-content: space-between; margin-top: .5rem; gap: .5rem; }",
    ".dang-fb-note { font-size: .68rem; color: var(--fg2); }",
    ".dang-fb-send {",
    "  font: inherit; font-size: .8rem; cursor: pointer; padding: .3rem .8rem;",
    "  border-radius: 6px; border: 1px solid var(--accent);",
    "  background: var(--accent); color: var(--on-accent);",
    "}",
    ".dang-fb-send:disabled { opacity: .5; cursor: default; }",
    ".dang-fb-status { font-size: .78rem; color: var(--fg); padding: .25rem 0; }",
    ".dang-fb-captcha { margin-top: .5rem; min-height: 0; }",
  ].join("\n");
  document.head.appendChild(style);

  // Read the element's text, ignoring our own injected controls.
  function excerptOf(el) {
    var clone = el.cloneNode(true);
    clone.querySelectorAll(".dang-fb").forEach(function (n) {
      n.remove();
    });
    return clone.textContent.trim().replace(/\s+/g, " ");
  }

  var openBubble = null;
  var openTarget = null;
  var openWidgetId = null;

  function closeBubble() {
    if (openBubble) {
      if (openWidgetId !== null && window.turnstile) {
        try {
          window.turnstile.remove(openWidgetId);
        } catch (e) {
          /* widget already gone */
        }
      }
      openWidgetId = null;
      openBubble.remove();
      openBubble = null;
      openTarget = null;
    }
  }

  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") closeBubble();
  });
  document.addEventListener("click", function (e) {
    if (
      openBubble &&
      !openBubble.contains(e.target) &&
      !e.target.classList.contains("dang-fb-link")
    ) {
      closeBubble();
    }
  });
  // The bubble is positioned against a fixed viewport point, so re-anchor it if
  // the page is scrolled or resized while it is open.
  function reposition() {
    if (openBubble && openTarget) positionBubble(openBubble, openTarget);
  }
  window.addEventListener("scroll", reposition, true);
  window.addEventListener("resize", reposition);

  // Place the bubble just under the target's top-right corner, clamped to the
  // viewport. Appended to <body> so it is never clipped by an element's
  // overflow (e.g. horizontally scrolling code blocks).
  function positionBubble(bubble, target) {
    var rect = target.getBoundingClientRect();
    var width = bubble.offsetWidth;
    var left = rect.right + window.scrollX - width;
    if (left < window.scrollX + 8) left = window.scrollX + 8;
    bubble.style.top = rect.top + window.scrollY + Math.min(rect.height, 28) + 6 + "px";
    bubble.style.left = left + "px";
  }

  function openFor(target) {
    closeBubble();

    var excerpt = excerptOf(target);

    var bubble = document.createElement("div");
    bubble.className = "dang-fb-bubble dang-fb";

    var quote = document.createElement("div");
    quote.className = "dang-fb-quote";
    quote.textContent = excerpt;

    var textarea = document.createElement("textarea");
    textarea.placeholder = "What's confusing, wrong, or missing here?";

    var row = document.createElement("div");
    row.className = "dang-fb-row";

    var note = document.createElement("span");
    note.className = "dang-fb-note";
    note.textContent = "Anonymous — no account or identity is recorded.";

    var send = document.createElement("button");
    send.className = "dang-fb-send";
    send.textContent = "Send";

    // The Turnstile widget renders here; its token is required to submit. This
    // is the only place a reader ever encounters a challenge.
    var captcha = document.createElement("div");
    captcha.className = "dang-fb-captcha";

    var err = document.createElement("div");
    err.className = "dang-fb-status";

    row.appendChild(note);
    row.appendChild(send);
    bubble.appendChild(quote);
    bubble.appendChild(textarea);
    bubble.appendChild(captcha);
    bubble.appendChild(err);
    bubble.appendChild(row);
    document.body.appendChild(bubble);
    openBubble = bubble;
    openTarget = target;
    positionBubble(bubble, target);
    textarea.focus();

    ensureTurnstile()
      .then(function (turnstile) {
        if (openTarget !== target) return; // bubble was closed before it loaded
        openWidgetId = turnstile.render(captcha, {
          sitekey: TURNSTILE_SITEKEY,
          theme: "auto",
          callback: function () {
            err.textContent = "";
            positionBubble(bubble, target);
          },
        });
        positionBubble(bubble, target);
      })
      .catch(function () {
        if (openTarget === target) {
          err.textContent = "Couldn't load verification. Reload and try again.";
        }
      });

    function showStatus(msg) {
      bubble.innerHTML = "";
      var s = document.createElement("div");
      s.className = "dang-fb-status";
      s.textContent = msg;
      bubble.appendChild(s);
      positionBubble(bubble, target);
    }

    function submit() {
      var message = textarea.value.trim();
      if (!message) {
        textarea.focus();
        return;
      }
      var token =
        window.turnstile && openWidgetId !== null
          ? window.turnstile.getResponse(openWidgetId)
          : "";
      if (!token) {
        err.textContent = "Please complete the verification, then Send.";
        return;
      }
      err.textContent = "";
      send.disabled = true;
      send.textContent = "Sending…";
      fetch(ENDPOINT, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          page: location.pathname,
          excerpt: excerpt,
          message: message,
          turnstileToken: token,
        }),
      })
        .then(function (res) {
          if (!res.ok) throw new Error("HTTP " + res.status);
          showStatus("Thanks! Your feedback was sent.");
          setTimeout(closeBubble, 1600);
        })
        .catch(function () {
          send.disabled = false;
          send.textContent = "Send";
          // Turnstile tokens are single-use; get a fresh one for the retry.
          if (window.turnstile && openWidgetId !== null) {
            window.turnstile.reset(openWidgetId);
          }
          err.textContent = "Couldn't send feedback. Please try again.";
        });
    }

    send.addEventListener("click", submit);
    textarea.addEventListener("keydown", function (e) {
      if ((e.metaKey || e.ctrlKey) && e.key === "Enter") submit();
    });
  }

  function attach(target, corner) {
    var link = document.createElement("button");
    link.type = "button";
    link.className = "dang-fb-link dang-fb" + (corner ? " dang-fb-link--corner" : "");
    link.setAttribute("aria-label", "Leave feedback on this");
    // The label lives in a child so the host itself can be zero-width: the
    // visible pill overflows the host instead of contributing to line width.
    var chip = document.createElement("span");
    chip.className = "dang-fb-chip";
    chip.textContent = "feedback";
    link.appendChild(chip);
    link.addEventListener("click", function (e) {
      e.stopPropagation();
      if (openTarget === target) {
        closeBubble();
      } else {
        openFor(target);
      }
    });
    target.appendChild(link);
  }

  // Only prose-bearing elements are worth commenting on. Skip navigational
  // chrome that happens to match the selectors above:
  //
  //   - the table of contents, rendered as <nav>, so every TOC entry is a
  //     <li> inside it;
  //   - any element whose entire text is a single link (the GitHub /
  //     pkg.go.dev header links, a bare "see [Page]" cross-ref, etc.) —
  //     there's no prose to give feedback on, just a destination.
  //
  // A list item like "see <a>Functions</a> for the full set" keeps its link:
  // its text is more than the link's, so it reads as prose.
  function isProse(el) {
    if (el.closest("nav")) return false;
    // The interactive playground is an editor, not prose — and a feedback link
    // appended to its code would leak into the editable source.
    if (el.closest(".dang-playground")) return false;
    var links = el.querySelectorAll("a");
    if (links.length === 1) {
      var linkText = links[0].textContent.trim().replace(/\s+/g, " ");
      if (linkText && excerptOf(el) === linkText) return false;
    }
    return true;
  }

  document.querySelectorAll(INLINE).forEach(function (el) {
    if (isProse(el)) attach(el, false);
  });
  document.querySelectorAll(BLOCK).forEach(function (el) {
    if (isProse(el)) attach(el, true);
  });
})();
