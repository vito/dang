// playground.js — progressive enhancement for \dang-playground blocks.
//
// Each block is authored as:
//
//   <div class="dang-playground" data-dang-playground>
//     <pre class="dang-playground-fallback">…source…</pre>
//   </div>
//
// Without JS the reader still sees the source. With JS we turn it into an
// editable, runnable widget backed by the Dang WebAssembly module
// (js/dang.wasm, built from cmd/dang-playground). The wasm is fetched lazily
// on the first Run so the page stays light.
//
// A block additionally marked data-dang-github gains a "Sign in with GitHub"
// control. After the OAuth web flow (see docs/functions/github/*), the access
// token is handed back in the URL fragment, stashed in sessionStorage, and
// passed to dangEval so an `import GitHub` in the snippet resolves against the
// live GitHub schema — introspection and queries run straight from the browser.

(function () {
  "use strict";

  // Resolve sibling assets relative to this script's own URL, so paths work
  // regardless of the page's location or base URL.
  var SELF = (document.currentScript && document.currentScript.src) || "";
  function asset(name) {
    try {
      return new URL(name, SELF || document.baseURI).href;
    } catch (e) {
      return "js/" + name;
    }
  }

  // ── GitHub auth (for data-dang-github playgrounds) ────────────────────────
  //
  // The token only ever lives in sessionStorage — cleared when the tab closes,
  // never sent anywhere but GitHub's own API. The OAuth code↔token exchange
  // (which needs the client secret) happens in the /github/* Cloudflare
  // Functions; everything here is just plumbing the resulting token around.

  var GH_TOKEN_KEY = "dang.gh_token";

  function ghToken() {
    try {
      return sessionStorage.getItem(GH_TOKEN_KEY) || "";
    } catch (e) {
      return "";
    }
  }

  function ghSetToken(t) {
    try {
      if (t) sessionStorage.setItem(GH_TOKEN_KEY, t);
      else sessionStorage.removeItem(GH_TOKEN_KEY);
    } catch (e) { /* sessionStorage unavailable; sign-in just won't persist */ }
  }

  // Kick off the OAuth web flow, returning to this page afterwards.
  function ghSignIn() {
    window.location.href =
      "/github/login?return=" + encodeURIComponent(window.location.pathname);
  }

  // On load, capture a token handed back in the URL fragment (#gh_token=…),
  // persist it, then strip the fragment so it doesn't linger in the URL/history.
  function ghCaptureToken() {
    var hash = window.location.hash || "";
    if (hash.indexOf("gh_token=") === -1) return;
    var params = new URLSearchParams(hash.replace(/^#/, ""));
    var t = params.get("gh_token");
    if (t) ghSetToken(t);
    var clean = window.location.pathname + window.location.search;
    try {
      window.history.replaceState(null, "", clean);
    } catch (e) {
      window.location.hash = "";
    }
  }

  // ── lazy WASM loader (shared across all playgrounds) ──────────────────────

  let dangPromise = null;
  let execPromise = null;

  // Inject Go's wasm_exec.js (defines the Go global) on demand, so pages
  // without a playground never load it.
  function loadWasmExec() {
    if (execPromise) return execPromise;
    execPromise = new Promise(function (resolve, reject) {
      if (typeof Go === "function") {
        resolve();
        return;
      }
      var s = document.createElement("script");
      s.src = asset("wasm_exec.js");
      s.onload = function () { resolve(); };
      s.onerror = function () { reject(new Error("failed to load wasm_exec.js")); };
      document.head.appendChild(s);
    });
    return execPromise;
  }

  function loadDang() {
    if (dangPromise) return dangPromise;
    dangPromise = loadWasmExec().then(function () {
      return new Promise(function (resolve, reject) {
      var go = new Go();
      // main() calls window.onDangReady once the exports are registered.
      window.onDangReady = function () {
        resolve({
          eval: window.dangEval,
          replEval: window.dangReplEval,
          replReset: window.dangReplReset,
        });
      };
      fetch(asset("dang.wasm"))
        .then(function (resp) {
          if (!resp.ok) throw new Error("failed to fetch dang.wasm (" + resp.status + ")");
          return resp.arrayBuffer();
        })
        .then(function (bytes) {
          return WebAssembly.instantiate(bytes, go.importObject);
        })
        .then(function (result) {
          go.run(result.instance); // runs until the module blocks; do not await
        })
        .catch(reject);
      });
    });
    return dangPromise;
  }

  // ── syntax highlighting (tree-sitter) ─────────────────────────────────────
  //
  // Reuses the in-repo grammar (tree-sitter-dang.wasm) and highlight query
  // (dang-highlights.scm) — the same ones the editors use — via web-tree-sitter.
  // Small (~140KB gzipped) and loaded eagerly, separate from the heavy eval
  // module, so editing feels instant.

  var tsPromise = null;
  var ts = null; // { parser, query } once ready

  function loadTreeSitter() {
    if (tsPromise) return tsPromise;
    tsPromise = (async function () {
      var mod = await import(asset("tree-sitter.js"));
      await mod.Parser.init({ locateFile: function (name) { return asset(name); } });
      var lang = await mod.Language.load(asset("tree-sitter-dang.wasm"));
      var parser = new mod.Parser();
      parser.setLanguage(lang);
      var scm = await fetch(asset("dang-highlights.scm")).then(function (r) { return r.text(); });
      var query = new mod.Query(lang, scm);
      ts = { parser: parser, query: query };
      return ts;
    })().catch(function (err) {
      // Highlighting is best-effort; fall back to plain text on failure.
      if (window.console) console.warn("dang playground: highlighting unavailable:", err);
      return null;
    });
    return tsPromise;
  }

  function escapeHtml(s) {
    return s.replace(/[&<>]/g, function (c) {
      return c === "&" ? "&amp;" : c === "<" ? "&lt;" : "&gt;";
    });
  }

  // Map a tree-sitter capture name (e.g. "constant.numeric") to a token class.
  function tokenClass(name) {
    var base = name.split(".")[0];
    switch (base) {
      case "keyword": return "tok-keyword";
      case "type": return "tok-type";
      case "string": return "tok-string";
      case "constant": return "tok-number";
      case "function": return "tok-function";
      case "comment": return "tok-comment";
      case "operator": return "tok-operator";
      case "label": return "tok-label";
      case "variable": return name === "variable.special" ? "tok-keyword" : "tok-variable";
      default: return "tok-punct";
    }
  }

  // Build highlighted HTML for source using the tree-sitter query captures.
  function highlightHtml(src) {
    if (!ts) return escapeHtml(src);
    var tree = ts.parser.parse(src);
    var caps = ts.query.captures(tree.rootNode);
    // Assign a class per character: wider captures first, narrower override.
    var names = new Array(src.length).fill(null);
    caps.sort(function (a, b) {
      var d = a.node.startIndex - b.node.startIndex;
      if (d) return d;
      return (b.node.endIndex - b.node.startIndex) - (a.node.endIndex - a.node.startIndex);
    });
    for (var i = 0; i < caps.length; i++) {
      var c = caps[i], cls = tokenClass(c.name);
      for (var j = c.node.startIndex; j < c.node.endIndex; j++) names[j] = cls;
    }
    tree.delete();
    // Coalesce runs of equal class into spans.
    var out = "", k = 0;
    while (k < src.length) {
      var cls2 = names[k], start = k;
      while (k < src.length && names[k] === cls2) k++;
      var chunk = escapeHtml(src.slice(start, k));
      out += cls2 ? '<span class="' + cls2 + '">' + chunk + "</span>" : chunk;
    }
    return out;
  }

  // ── output rendering ──────────────────────────────────────────────────────

  var STAGE_LABEL = { parse: "Parse error", type: "Type error", eval: "Runtime error", auth: "GitHub error" };

  function renderOutput(out, res) {
    out.innerHTML = "";
    out.classList.remove("is-error", "is-empty");

    if (res.output) {
      var pre = document.createElement("div");
      pre.className = "dang-playground-stdout";
      pre.textContent = res.output;
      out.appendChild(pre);
    }

    var line = document.createElement("div");
    if (res.ok) {
      line.className = "dang-playground-result";
      line.textContent = "=> " + res.value;
    } else {
      out.classList.add("is-error");
      line.className = "dang-playground-error";
      var label = STAGE_LABEL[res.stage] || "Error";
      line.textContent = label + ": " + res.error;
    }
    out.appendChild(line);
  }

  // ── widget construction ───────────────────────────────────────────────────

  function enhance(container) {
    var fallback = container.querySelector(".dang-playground-fallback");
    if (!fallback) return;
    // Read the source without any controls another script may have injected
    // (e.g. the feedback link), so they don't leak into the editable code.
    var seed = fallback.cloneNode(true);
    seed.querySelectorAll(".dang-fb").forEach(function (n) { n.remove(); });
    var source = seed.textContent.replace(/\s+$/, "");
    var isGitHub = container.hasAttribute("data-dang-github");
    container.innerHTML = "";

    // Toolbar.
    var bar = document.createElement("div");
    bar.className = "dang-playground-bar";
    var label = document.createElement("span");
    label.className = "dang-playground-label";
    label.textContent = "Dang · runs in your browser";
    var spacer = document.createElement("span");
    spacer.style.flex = "1";
    // GitHub auth control (only on data-dang-github blocks). updateAuth()
    // reflects the current sign-in state; it's re-run after sign-in/out.
    var authBtn = null;
    function updateAuth() {
      if (!authBtn) return;
      if (ghToken()) {
        authBtn.textContent = "Sign out of GitHub";
        authBtn.title = "Signed in. Click to forget the token.";
      } else {
        authBtn.textContent = "Sign in with GitHub";
        authBtn.title = "Authorize so `import GitHub` can reach the API.";
      }
    }
    if (isGitHub) {
      authBtn = document.createElement("button");
      authBtn.className = "dang-playground-btn dang-playground-github";
      authBtn.type = "button";
      authBtn.addEventListener("click", function () {
        if (ghToken()) {
          ghSetToken("");
          updateAuth();
        } else {
          ghSignIn();
        }
      });
      updateAuth();
    }
    var resetBtn = document.createElement("button");
    resetBtn.className = "dang-playground-btn dang-playground-reset";
    resetBtn.type = "button";
    resetBtn.textContent = "Reset";
    var runBtn = document.createElement("button");
    runBtn.className = "dang-playground-btn dang-playground-run";
    runBtn.type = "button";
    runBtn.textContent = "Run ▶";
    bar.appendChild(label);
    bar.appendChild(spacer);
    if (authBtn) bar.appendChild(authBtn);
    bar.appendChild(resetBtn);
    bar.appendChild(runBtn);

    // Editor: a transparent textarea layered over a highlighted <pre>. Both
    // share identical metrics so the caret lines up with the colored text.
    var editorWrap = document.createElement("div");
    editorWrap.className = "dang-playground-editor";

    var highlight = document.createElement("pre");
    highlight.className = "dang-playground-highlight";
    highlight.setAttribute("aria-hidden", "true");

    var editor = document.createElement("textarea");
    editor.className = "dang-playground-input";
    editor.spellcheck = false;
    editor.setAttribute("autocomplete", "off");
    editor.setAttribute("autocapitalize", "off");
    editor.setAttribute("autocorrect", "off");
    editor.value = source;

    editorWrap.appendChild(highlight);
    editorWrap.appendChild(editor);

    // Output.
    var output = document.createElement("div");
    output.className = "dang-playground-output is-empty";
    output.textContent = "";

    container.appendChild(bar);
    container.appendChild(editorWrap);
    container.appendChild(output);

    function rehighlight() {
      highlight.innerHTML = highlightHtml(editor.value);
    }
    function autosize() {
      editor.style.height = "auto";
      editor.style.height = editor.scrollHeight + "px";
    }
    rehighlight();
    autosize();
    editor.addEventListener("input", function () {
      rehighlight();
      autosize();
    });
    // Highlight as soon as the (small) grammar finishes loading.
    loadTreeSitter().then(function () { rehighlight(); });

    // Tab inserts two spaces instead of moving focus.
    editor.addEventListener("keydown", function (e) {
      if (e.key === "Tab") {
        e.preventDefault();
        var s = editor.selectionStart, en = editor.selectionEnd;
        editor.value = editor.value.slice(0, s) + "  " + editor.value.slice(en);
        editor.selectionStart = editor.selectionEnd = s + 2;
        rehighlight();
        autosize();
      }
      // Cmd/Ctrl+Enter runs.
      if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        run();
      }
    });

    resetBtn.addEventListener("click", function () {
      editor.value = source;
      rehighlight();
      autosize();
      output.className = "dang-playground-output is-empty";
      output.innerHTML = "";
    });

    var running = false;
    function run() {
      if (running) return;
      var token = isGitHub ? ghToken() : "";
      if (isGitHub && !token) {
        // Don't bother compiling: `import GitHub` can't resolve without a token.
        output.className = "dang-playground-output is-error";
        output.innerHTML = "";
        var hint = document.createElement("div");
        hint.className = "dang-playground-error";
        hint.textContent = "Sign in with GitHub first — `import GitHub` needs an authorized token.";
        output.appendChild(hint);
        return;
      }
      running = true;
      runBtn.disabled = true;
      runBtn.textContent = "Running…";
      output.className = "dang-playground-output";
      output.textContent = isGitHub ? "Querying GitHub…" : "Compiling…";
      loadDang()
        .then(function (dang) {
          // dang.eval returns a Promise (it may hit the network for GitHub).
          return dang.eval(editor.value, token);
        })
        .then(function (res) {
          output.textContent = "";
          renderOutput(output, res);
        })
        .catch(function (err) {
          output.className = "dang-playground-output is-error";
          output.textContent = "Failed to load playground: " + (err && err.message ? err.message : err);
        })
        .then(function () {
          running = false;
          runBtn.disabled = false;
          runBtn.textContent = "Run ▶";
        });
    }
    runBtn.addEventListener("click", run);
  }

  // ── REPL ──────────────────────────────────────────────────────────────────
  //
  // A read-eval-print loop layered on the same wasm + highlighting stack as the
  // playground. Each accepted entry is appended to a transcript and evaluated
  // against a persistent, long-running session held in the wasm module (see
  // dangReplEval in cmd/dang-playground) — definitions accumulate without
  // re-running earlier entries. The prompt and "=>" result match the CLI REPL
  // so the two feel like the same tool.

  // Each REPL component gets a distinct session id so multiple REPLs on one
  // page keep independent state. The wasm side creates the session lazily.
  var nextReplSession = 0;

  // Heuristic: is the bracket nesting of src balanced? Used to decide whether a
  // bare Enter should evaluate (balanced) or insert a continuation newline. We
  // skip over string and comment contents so their brackets don't count. It's
  // best-effort — Shift+Enter always inserts a newline and Ctrl/Cmd+Enter (and
  // the Run button) always evaluate, so a wrong guess is never a dead end.
  function bracketsBalanced(src) {
    var depth = 0, i = 0, n = src.length;
    while (i < n) {
      var c = src[i];
      if (c === "#") { // line comment
        while (i < n && src[i] !== "\n") i++;
        continue;
      }
      if (c === '"' || c === "`") { // string literal (opaque, incl. interpolation)
        var q = c;
        i++;
        while (i < n && src[i] !== q) { if (src[i] === "\\") i++; i++; }
        i++;
        continue;
      }
      if (c === "(" || c === "[" || c === "{") depth++;
      else if (c === ")" || c === "]" || c === "}") depth--;
      i++;
    }
    return depth <= 0;
  }

  // Render one REPL entry's result into its output element. Like renderOutput,
  // but a successful entry with no value and no output shows nothing (so plain
  // declarations don't leave a bare "=>").
  function renderReplOutput(out, res) {
    out.innerHTML = "";
    out.classList.remove("is-error", "is-empty");
    if (res.output) {
      var pre = document.createElement("div");
      pre.className = "dang-playground-stdout";
      pre.textContent = res.output;
      out.appendChild(pre);
    }
    if (res.ok) {
      if (res.value !== "") {
        var line = document.createElement("div");
        line.className = "dang-playground-result";
        line.textContent = "=> " + res.value;
        out.appendChild(line);
      }
    } else {
      out.classList.add("is-error");
      var err = document.createElement("div");
      err.className = "dang-playground-error";
      var label = STAGE_LABEL[res.stage] || "Error";
      err.textContent = label + ": " + res.error;
      out.appendChild(err);
    }
    if (!out.firstChild) out.classList.add("is-empty");
  }

  function enhanceRepl(container) {
    var fallback = container.querySelector(".dang-repl-fallback");
    if (!fallback) return;
    var seed = fallback.cloneNode(true);
    seed.querySelectorAll(".dang-fb").forEach(function (n) { n.remove(); });
    var seedSource = seed.textContent.replace(/\s+$/, "");
    container.innerHTML = "";

    // Toolbar.
    var bar = document.createElement("div");
    bar.className = "dang-repl-bar";
    var label = document.createElement("span");
    label.className = "dang-repl-label";
    label.textContent = "Dang REPL · Enter to run · Shift+Enter for newline";
    var spacer = document.createElement("span");
    spacer.style.flex = "1";
    var resetBtn = document.createElement("button");
    resetBtn.className = "dang-playground-btn dang-repl-reset";
    resetBtn.type = "button";
    resetBtn.textContent = "Reset";
    var runBtn = document.createElement("button");
    runBtn.className = "dang-playground-btn dang-playground-run dang-repl-run";
    runBtn.type = "button";
    runBtn.textContent = "Run ▶";
    bar.appendChild(label);
    bar.appendChild(spacer);
    bar.appendChild(resetBtn);
    bar.appendChild(runBtn);

    // Body: a scrolling transcript followed by the live input row.
    var body = document.createElement("div");
    body.className = "dang-repl-body";

    var transcript = document.createElement("div");
    transcript.className = "dang-repl-transcript";

    var inputRow = document.createElement("div");
    inputRow.className = "dang-repl-inputrow";
    var prompt = document.createElement("span");
    prompt.className = "dang-repl-prompt";
    prompt.textContent = "dang>";
    var editorWrap = document.createElement("div");
    editorWrap.className = "dang-repl-editor";
    var highlight = document.createElement("pre");
    highlight.className = "dang-repl-highlight";
    highlight.setAttribute("aria-hidden", "true");
    var input = document.createElement("textarea");
    input.className = "dang-repl-input";
    input.spellcheck = false;
    input.setAttribute("autocomplete", "off");
    input.setAttribute("autocapitalize", "off");
    input.setAttribute("autocorrect", "off");
    input.setAttribute("rows", "1");
    input.value = seedSource;
    editorWrap.appendChild(highlight);
    editorWrap.appendChild(input);
    inputRow.appendChild(prompt);
    inputRow.appendChild(editorWrap);

    body.appendChild(transcript);
    body.appendChild(inputRow);
    container.appendChild(bar);
    container.appendChild(body);

    // Handle for this REPL's persistent wasm-side session.
    var sessionId = nextReplSession++;

    function rehighlight() {
      highlight.innerHTML = highlightHtml(input.value);
    }
    function autosize() {
      input.style.height = "auto";
      input.style.height = input.scrollHeight + "px";
    }
    rehighlight();
    autosize();
    input.addEventListener("input", function () {
      rehighlight();
      autosize();
    });
    loadTreeSitter().then(function () { rehighlight(); });

    // Append a finished entry (highlighted source + its result) to the
    // transcript. Returns the result element so callers can fill it in.
    function appendEntry(src, res) {
      var entry = document.createElement("div");
      entry.className = "dang-repl-entry";

      var code = document.createElement("div");
      code.className = "dang-repl-code";
      var p = document.createElement("span");
      p.className = "dang-repl-prompt";
      p.textContent = "dang>";
      var hl = document.createElement("pre");
      hl.className = "dang-repl-codehl";
      hl.innerHTML = highlightHtml(src);
      code.appendChild(p);
      code.appendChild(hl);

      var out = document.createElement("div");
      out.className = "dang-repl-out";
      renderReplOutput(out, res);

      entry.appendChild(code);
      entry.appendChild(out);
      transcript.appendChild(entry);
      // Keep the live input in view as the transcript grows.
      input.scrollIntoView({ block: "nearest" });
      return entry;
    }

    var running = false;
    function evalEntry() {
      if (running) return;
      var src = input.value.replace(/\s+$/, "");
      if (!src) return;
      running = true;
      runBtn.disabled = true;
      input.disabled = true;
      var pending = appendEntry(src, { ok: true, value: "", output: "" });
      var pendingOut = pending.querySelector(".dang-repl-out");
      pendingOut.classList.remove("is-empty");
      pendingOut.textContent = "Running…";
      loadDang()
        .then(function (dang) {
          var res = dang.replEval(sessionId, src);
          renderReplOutput(pendingOut, res);
          input.value = "";
          rehighlight();
          autosize();
        })
        .catch(function (err) {
          renderReplOutput(pendingOut, {
            ok: false, stage: "", value: "", output: "",
            error: "Failed to load REPL: " + (err && err.message ? err.message : err),
          });
        })
        .then(function () {
          running = false;
          runBtn.disabled = false;
          input.disabled = false;
          input.focus();
        });
    }

    input.addEventListener("keydown", function (e) {
      if (e.key === "Tab") {
        e.preventDefault();
        var s = input.selectionStart, en = input.selectionEnd;
        input.value = input.value.slice(0, s) + "  " + input.value.slice(en);
        input.selectionStart = input.selectionEnd = s + 2;
        rehighlight();
        autosize();
        return;
      }
      if (e.key === "Enter") {
        // Shift+Enter: force a newline (continuation). Ctrl/Cmd+Enter: force
        // run. Bare Enter: run when the input looks complete, else newline.
        if (e.shiftKey) return; // default inserts the newline
        if (e.metaKey || e.ctrlKey || bracketsBalanced(input.value)) {
          e.preventDefault();
          evalEntry();
        }
      }
    });

    runBtn.addEventListener("click", evalEntry);
    resetBtn.addEventListener("click", function () {
      // Drop the persistent session's state. Only touch the module if it's
      // already loaded — a reset before the first Run has nothing to clear and
      // shouldn't pull the wasm down early.
      if (window.dangReady) {
        loadDang().then(function (dang) { dang.replReset(sessionId); });
      }
      transcript.innerHTML = "";
      input.value = seedSource;
      input.disabled = false;
      rehighlight();
      autosize();
      input.focus();
    });
  }

  function init() {
    var blocks = document.querySelectorAll("[data-dang-playground]");
    for (var i = 0; i < blocks.length; i++) enhance(blocks[i]);
    var repls = document.querySelectorAll("[data-dang-repl]");
    for (var j = 0; j < repls.length; j++) enhanceRepl(repls[j]);
  }

  // Capture any OAuth token handed back in the fragment before enhancing, so
  // the first paint of a GitHub block already reflects the signed-in state.
  ghCaptureToken();

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
