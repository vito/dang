// Client-side full-text search over the Booklit-generated search_index.json.
//
// Press Ctrl+K (or Cmd+K on macOS), or click the sidebar search box, to open a
// command-palette style overlay. Typing filters section entries by title and
// body text; arrow keys move the selection and Enter navigates to the section.
//
// The index is fetched lazily the first time the palette is opened, so it adds
// nothing to initial page load. Everything (DOM + styles) is injected from
// here, so the only template hook required is loading this script.
(function () {
  "use strict";

  var INDEX_URL = "search_index.json";

  // ---- styles ------------------------------------------------------------

  var style = document.createElement("style");
  style.textContent = [
    ".dang-search-trigger {",
    "  display: flex; align-items: center; gap: .5rem; width: 100%;",
    "  margin: 0 0 1rem; padding: .4rem .55rem;",
    "  background: var(--bg3); color: var(--fg2); border: 1px solid var(--code-border);",
    "  border-radius: 6px; font: inherit; font-size: .8rem; cursor: pointer; text-align: left;",
    "}",
    ".dang-search-trigger:hover { color: var(--fg); border-color: var(--accent); }",
    ".dang-search-trigger .kbd { margin-left: auto; font-family: 'JetBrains Mono', monospace;",
    "  font-size: .7rem; color: var(--fg2); border: 1px solid var(--code-border);",
    "  border-radius: 4px; padding: .05rem .3rem; }",
    "",
    ".dang-search-overlay {",
    "  position: fixed; inset: 0; z-index: 1000; display: none;",
    "  background: rgba(0,0,0,.5); padding: 10vh 1rem 1rem; overflow: hidden;",
    "}",
    ".dang-search-overlay.open { display: block; }",
    ".dang-search-box {",
    "  max-width: 640px; margin: 0 auto; background: var(--bg2);",
    "  border: 1px solid var(--code-border); border-radius: 10px; overflow: hidden;",
    "  box-shadow: 0 16px 48px rgba(0,0,0,.4); display: flex; flex-direction: column;",
    "  max-height: 70vh;",
    "}",
    ".dang-search-box input {",
    "  width: 100%; padding: .9rem 1rem; background: transparent; border: none;",
    "  border-bottom: 1px solid var(--code-border); color: var(--fg);",
    "  font: inherit; font-size: 1rem; outline: none;",
    "}",
    ".dang-search-results { list-style: none; margin: 0; padding: .35rem; overflow-y: auto; }",
    ".dang-search-results li { margin: 0; }",
    ".dang-search-results a {",
    "  display: block; padding: .5rem .65rem; border-radius: 6px; color: var(--fg);",
    "  text-decoration: none;",
    "}",
    ".dang-search-results li.active a, .dang-search-results a:hover { background: var(--bg3); }",
    ".dang-search-results .title { font-size: .9rem; font-weight: 500; color: var(--fg); }",
    ".dang-search-results .crumb { font-size: .72rem; color: var(--accent2); margin-bottom: .15rem; }",
    ".dang-search-results .snippet { font-size: .78rem; color: var(--fg); line-height: 1.45;",
    "  display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }",
    ".dang-search-results mark { background: transparent; color: var(--accent2); font-weight: 600; }",
    ".dang-search-empty { padding: 1.25rem; text-align: center; color: var(--fg2); font-size: .85rem; }",
  ].join("\n");
  document.head.appendChild(style);

  // ---- DOM ---------------------------------------------------------------

  var overlay = document.createElement("div");
  overlay.className = "dang-search-overlay";
  overlay.innerHTML =
    '<div class="dang-search-box" role="dialog" aria-modal="true" aria-label="Search docs">' +
    '<input type="text" placeholder="Search the docs…" autocomplete="off" spellcheck="false" aria-label="Search">' +
    '<ul class="dang-search-results"></ul>' +
    "</div>";
  document.body.appendChild(overlay);

  var box = overlay.querySelector(".dang-search-box");
  var input = overlay.querySelector("input");
  var resultsEl = overlay.querySelector(".dang-search-results");

  // A search box in the sidebar, both for discoverability and as a click
  // target. Falls back to nothing if the sidebar isn't present.
  var sidebar = document.querySelector("nav.sidebar");
  if (sidebar) {
    var trigger = document.createElement("button");
    trigger.type = "button";
    trigger.className = "dang-search-trigger";
    var isMac = /Mac|iPhone|iPad/.test(navigator.platform);
    trigger.innerHTML =
      "<span>Search</span><span class=\"kbd\">" + (isMac ? "⌘K" : "Ctrl K") + "</span>";
    trigger.addEventListener("click", open);
    // Place the trigger just above the nav list. `.brand` is nested inside
    // `.sidebar-head`, so inserting relative to it on `sidebar` would throw
    // ("Child to insert before is not a child of this node").
    var nav = sidebar.querySelector(".sidebar-nav");
    if (nav) {
      sidebar.insertBefore(trigger, nav);
    } else {
      sidebar.appendChild(trigger);
    }
  }

  // ---- index -------------------------------------------------------------

  var entries = null; // array of {title, location, page, text}
  var indexPromise = null;

  function loadIndex() {
    if (indexPromise) return indexPromise;
    indexPromise = fetch(INDEX_URL)
      .then(function (r) {
        if (!r.ok) throw new Error("failed to load search index");
        return r.json();
      })
      .then(function (data) {
        var scratch = document.createElement("div");
        entries = Object.keys(data).map(function (key) {
          var e = data[key];
          // The indexed text is rendered HTML; strip it down to plain text
          // for matching and snippets.
          scratch.innerHTML = e.text || "";
          var text = (scratch.textContent || "").replace(/\s+/g, " ").trim();
          var page = (e.location || "").split("#")[0].replace(/\.html$/, "");
          return {
            title: e.title || key,
            location: e.location || "",
            page: page,
            text: text,
            haystack: ((e.title || "") + " " + text).toLowerCase(),
          };
        });
        return entries;
      })
      .catch(function (err) {
        indexPromise = null; // allow retry on next open
        throw err;
      });
    return indexPromise;
  }

  // ---- searching ---------------------------------------------------------

  var MAX_RESULTS = 40;

  function search(query) {
    var terms = query.toLowerCase().split(/\s+/).filter(Boolean);
    if (!terms.length || !entries) return [];
    var scored = [];
    for (var i = 0; i < entries.length; i++) {
      var e = entries[i];
      var ok = true;
      var score = 0;
      var title = e.title.toLowerCase();
      for (var t = 0; t < terms.length; t++) {
        var term = terms[t];
        if (e.haystack.indexOf(term) === -1) { ok = false; break; }
        var ti = title.indexOf(term);
        if (ti === 0) score += 12;
        else if (ti > 0) score += 6;
        else score += 1;
      }
      if (ok) scored.push({ entry: e, score: score, terms: terms });
    }
    scored.sort(function (a, b) {
      if (b.score !== a.score) return b.score - a.score;
      return a.entry.title.length - b.entry.title.length;
    });
    return scored.slice(0, MAX_RESULTS);
  }

  function escapeHtml(s) {
    return s.replace(/[&<>"]/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c];
    });
  }

  // Highlight every term occurrence in an already-escaped string.
  function highlight(escaped, terms) {
    var out = escaped;
    for (var i = 0; i < terms.length; i++) {
      var re = new RegExp("(" + terms[i].replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + ")", "ig");
      out = out.replace(re, "\u0000$1\u0001");
    }
    return out.split("\u0000").join("<mark>").split("\u0001").join("</mark>");
  }

  // Build a snippet centred on the first matching term.
  function snippet(text, terms) {
    var lower = text.toLowerCase();
    var at = -1;
    for (var i = 0; i < terms.length; i++) {
      var idx = lower.indexOf(terms[i]);
      if (idx !== -1 && (at === -1 || idx < at)) at = idx;
    }
    if (at === -1) return text.slice(0, 140);
    var start = Math.max(0, at - 50);
    var end = Math.min(text.length, at + 110);
    var s = (start > 0 ? "…" : "") + text.slice(start, end) + (end < text.length ? "…" : "");
    return s;
  }

  var active = -1;
  var current = [];

  function render(query) {
    current = search(query);
    active = current.length ? 0 : -1;
    if (!query) {
      resultsEl.innerHTML = "";
      return;
    }
    if (!current.length) {
      resultsEl.innerHTML = '<li class="dang-search-empty">No results for “' +
        escapeHtml(query) + "”</li>";
      return;
    }
    resultsEl.innerHTML = current.map(function (r, i) {
      var e = r.entry;
      var title = highlight(escapeHtml(e.title), r.terms);
      var snip = highlight(escapeHtml(snippet(e.text, r.terms)), r.terms);
      return '<li' + (i === active ? ' class="active"' : "") + '>' +
        '<a href="' + escapeHtml(e.location) + '">' +
        '<div class="crumb">' + escapeHtml(e.page) + "</div>" +
        '<div class="title">' + title + "</div>" +
        '<div class="snippet">' + snip + "</div>" +
        "</a></li>";
    }).join("");
  }

  function setActive(i) {
    var items = resultsEl.querySelectorAll("li");
    if (!items.length) return;
    if (active >= 0 && items[active]) items[active].classList.remove("active");
    active = (i + items.length) % items.length;
    var el = items[active];
    if (el) {
      el.classList.add("active");
      el.scrollIntoView({ block: "nearest" });
    }
  }

  function go() {
    var items = resultsEl.querySelectorAll("li.active a, li a");
    var el = active >= 0 ? resultsEl.querySelectorAll("li")[active] : null;
    var link = el ? el.querySelector("a") : (items[0] || null);
    if (link) window.location.href = link.getAttribute("href");
  }

  // ---- open / close ------------------------------------------------------

  function open() {
    overlay.classList.add("open");
    input.value = "";
    resultsEl.innerHTML = "";
    input.focus();
    loadIndex().catch(function () {
      resultsEl.innerHTML = '<li class="dang-search-empty">Couldn’t load the search index.</li>';
    });
  }

  function close() {
    overlay.classList.remove("open");
  }

  function isOpen() {
    return overlay.classList.contains("open");
  }

  input.addEventListener("input", function () {
    render(input.value.trim());
  });

  input.addEventListener("keydown", function (ev) {
    if (ev.key === "ArrowDown") { ev.preventDefault(); setActive(active + 1); }
    else if (ev.key === "ArrowUp") { ev.preventDefault(); setActive(active - 1); }
    else if (ev.key === "Enter") { ev.preventDefault(); go(); }
    else if (ev.key === "Escape") { ev.preventDefault(); close(); }
  });

  overlay.addEventListener("mousedown", function (ev) {
    if (!box.contains(ev.target)) close();
  });

  document.addEventListener("keydown", function (ev) {
    if ((ev.ctrlKey || ev.metaKey) && (ev.key === "k" || ev.key === "K")) {
      ev.preventDefault();
      if (isOpen()) close(); else open();
    } else if (ev.key === "Escape" && isOpen()) {
      close();
    }
  });
})();
