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
          literateEval: window.dangLiterateEval,
          literateFailEval: window.dangLiterateFailEval,
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

  // Map a tree-sitter capture name (e.g. "constant.numeric") to a token
  // class, themed by syntax.css. This mirrors captureClass in
  // docs/go/highlight.go (the two must stay in lockstep), so the editors
  // match the statically-highlighted blocks span for span.
  function tokenClass(name) {
    switch (name) {
      case "variable.special": return "tok-self";
      case "function.builtin": return "tok-builtin";
      case "function.macro": return "tok-directive";
      case "string.escape": return "tok-escape";
      case "property": return "tok-property";
      case "label": return "tok-label";
      case "type": return "tok-type";
    }
    switch (name.split(".")[0]) {
      case "keyword": return "tok-keyword";
      case "constant": case "number": return "tok-number";
      case "string": return "tok-string";
      case "comment": return "tok-comment";
      case "operator": return "tok-operator";
      case "punctuation": return "tok-punct";
      case "function": return "tok-function";
      case "variable": return "tok-variable";
    }
    return null;
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
      if (!cls) continue; // unmapped captures (e.g. @error) stay unstyled
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

  // ── editor autosizing ─────────────────────────────────────────────────────
  //
  // Every editor textarea autosizes to its content by measuring scrollHeight.
  // The measurement is only valid for the width (and font) it was taken at:
  // wrapping changes when the viewport narrows and again when the monospace
  // webfont arrives. Each widget registers its autosize here so one shared,
  // frame-throttled pass can re-measure them all on those events.

  var autosizers = [];
  var autosizeQueued = false;
  function autosizeAll() {
    if (autosizeQueued) return;
    autosizeQueued = true;
    requestAnimationFrame(function () {
      autosizeQueued = false;
      for (var i = 0; i < autosizers.length; i++) autosizers[i]();
    });
  }
  window.addEventListener("resize", autosizeAll);
  if (document.fonts && document.fonts.ready) document.fonts.ready.then(autosizeAll);

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
    // The fallback is highlighted at build time with the same tok-* spans
    // tree-sitter produces, so it can seed the editor's highlight layer and
    // the code stays colored from first paint until the grammar loads.
    var seedCode = seed.querySelector("code");
    var seedHtml = seedCode ? seedCode.innerHTML : null;
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
    // rows=1, like the REPL input: the default two-row minimum would keep a
    // single-line snippet two lines tall even after autosize.
    editor.setAttribute("rows", "1");
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
    autosizers.push(autosize);
    if (seedHtml && !ts) {
      highlight.innerHTML = seedHtml;
    } else {
      rehighlight();
    }
    autosize();
    editor.addEventListener("input", function () {
      rehighlight();
      autosize();
    });
    // Re-render as soon as the (small) grammar finishes loading, taking over
    // from the build-time seed.
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
    // Build-time tok-* spans, used to seed the input row's highlight layer
    // (see enhance()).
    var seedCode = seed.querySelector("code");
    var seedHtml = seedCode ? seedCode.innerHTML : null;
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
    // Re-highlight the live input plus any transcript entries rendered before
    // tree-sitter finished loading. The lazy stdlib REPLs evaluate their seed
    // synchronously on Run — before loadTreeSitter() resolves — so that first
    // entry comes out as plain text and must be repainted once highlighting is
    // ready. (Each codehl's textContent is exactly its source.)
    function rehighlightAll() {
      rehighlight();
      var hls = transcript.querySelectorAll(".dang-repl-codehl");
      for (var i = 0; i < hls.length; i++) {
        hls[i].innerHTML = highlightHtml(hls[i].textContent);
      }
    }
    function autosize() {
      input.style.height = "auto";
      input.style.height = input.scrollHeight + "px";
    }
    autosizers.push(autosize);
    if (seedHtml && !ts) {
      highlight.innerHTML = seedHtml;
    } else {
      rehighlight();
    }
    autosize();
    input.addEventListener("input", function () {
      rehighlight();
      autosize();
    });
    loadTreeSitter().then(rehighlightAll);

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

    // Command history: submitted sources, newest last. historyIndex points at
    // the recalled entry; history.length means the live draft being typed.
    var history = [];
    var historyIndex = 0;
    var draft = "";
    function recall(val) {
      input.value = val;
      rehighlight();
      autosize();
      input.selectionStart = input.selectionEnd = val.length;
    }

    var running = false;
    function evalEntry() {
      if (running) return;
      var src = input.value.replace(/\s+$/, "");
      if (!src) return;
      if (history[history.length - 1] !== src) history.push(src);
      historyIndex = history.length;
      draft = "";
      running = true;
      runBtn.disabled = true;
      input.disabled = true;
      var pending = appendEntry(src, { ok: true, value: "", output: "" });
      var pendingOut = pending.querySelector(".dang-repl-out");
      pendingOut.classList.remove("is-empty");
      pendingOut.textContent = "Running…";
      // Clear the input now that the source is captured and shown in the
      // transcript, rather than waiting for the (possibly slow) eval to finish —
      // the prompt shouldn't keep showing what's already running.
      input.value = "";
      rehighlight();
      autosize();
      loadDang()
        .then(function (dang) {
          var res = dang.replEval(sessionId, src);
          renderReplOutput(pendingOut, res);
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
      if (e.key === "ArrowUp" && !e.shiftKey && !e.metaKey && !e.ctrlKey && !e.altKey) {
        // Recall older history — but only from the first line, so multi-line
        // editing still moves the caret up a line as usual.
        if (input.value.slice(0, input.selectionStart).indexOf("\n") !== -1) return;
        if (historyIndex > 0) {
          if (historyIndex === history.length) draft = input.value;
          historyIndex--;
          e.preventDefault();
          recall(history[historyIndex]);
        }
        return;
      }
      if (e.key === "ArrowDown" && !e.shiftKey && !e.metaKey && !e.ctrlKey && !e.altKey) {
        // Walk back toward the live draft, only from the last line.
        if (input.value.slice(input.selectionEnd).indexOf("\n") !== -1) return;
        if (historyIndex < history.length) {
          historyIndex++;
          e.preventDefault();
          recall(historyIndex === history.length ? draft : history[historyIndex]);
        }
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
      history = [];
      historyIndex = 0;
      draft = "";
      input.value = seedSource;
      input.disabled = false;
      rehighlight();
      autosize();
      input.focus();
    });
  }

  // A lazy REPL — used by the stdlib reference, one per builtin — stays a
  // static, highlighted snippet (its fallback) until the reader clicks Run.
  // Only then does it become a live REPL (enhanceRepl) seeded with the example,
  // which it evaluates once. Deferring keeps a page full of examples cheap: no
  // textareas or wasm until something is actually run.
  function enhanceLazyRepl(container) {
    var fallback = container.querySelector(".dang-repl-fallback");
    if (!fallback) return;

    var bar = document.createElement("div");
    bar.className = "stdlib-example-bar";
    var label = document.createElement("span");
    label.className = "stdlib-example-label";
    label.textContent = "Example";
    var spacer = document.createElement("span");
    spacer.style.flex = "1";
    var runBtn = document.createElement("button");
    runBtn.className = "dang-playground-btn dang-playground-run";
    runBtn.type = "button";
    runBtn.textContent = "Run ▶";
    bar.appendChild(label);
    bar.appendChild(spacer);
    bar.appendChild(runBtn);
    container.appendChild(bar);

    // Warm the (shared, cached) tree-sitter highlighter on intent, so it's
    // ready by the time the click lands. Without this the lazy example is the
    // first thing on a stdlib page to load tree-sitter, and enhanceRepl below
    // builds *and* runs the seed synchronously — before highlighting is ready —
    // so the first transcript entry would paint plain. Still lazy: nothing
    // loads on mere page load, only once the reader reaches for Run.
    var warmed = false;
    function warm() {
      if (warmed) return;
      warmed = true;
      loadTreeSitter();
    }
    runBtn.addEventListener("pointerenter", warm);
    runBtn.addEventListener("focus", warm);

    runBtn.addEventListener("click", function () {
      // enhanceRepl reads the fallback for its seed, wipes the container, and
      // builds the live REPL; then we trigger its Run to show the result.
      enhanceRepl(container);
      var liveRun = container.querySelector(".dang-repl-run");
      if (liveRun) liveRun.click();
    });
  }

  // ── literate blocks ───────────────────────────────────────────────────────
  //
  // \dang-literate blocks (docs/go/literate.go) arrive with their output
  // already baked in at build time, so the page is complete without JS. The
  // enhancement makes them editable: every literate block on the page shares
  // one Dang environment, evaluated top to bottom — Run (or Ctrl/Cmd+Enter)
  // resets that shared session and replays the whole chain with the editors'
  // current contents, updating each block's output along the way. Editing a
  // block dims its output and every output below it until the next replay,
  // since anything downstream may depend on the edit.

  function enhanceLiterateChain(containers) {
    var sessionId = nextReplSession++;
    var chain = []; // { editor, out, runBtn } in document order

    var running = false;
    function runChain(origin) {
      if (running) return;
      running = true;
      chain.forEach(function (b) { b.runBtn.disabled = true; });
      if (origin) origin.runBtn.textContent = "Running…";
      loadDang()
        .then(function (dang) {
          dang.replReset(sessionId);
          var failed = false;
          chain.forEach(function (b) {
            if (failed) {
              // State below a failed block is unknowable; dim its last-known
              // output rather than pretend it still holds.
              b.out.classList.add("is-stale");
              return;
            }
            var source = b.editor.value.replace(/\s+$/, "");
            if (b.expectFailure) {
              // Expected-failure blocks (```dang-failure) evaluate against
              // throwaway forks of the session, so they never poison the
              // chain: the error is their output, and even an (unexpected)
              // success contributes no state.
              var fres = dang.literateFailEval(sessionId, source);
              if (fres.ok) {
                renderReplOutput(b.out, {
                  ok: false, stage: "", value: "", output: fres.output,
                  error: "expected this block to fail, but it succeeded" +
                    (fres.value !== "" ? " with => " + fres.value : ""),
                });
              } else {
                renderReplOutput(b.out, fres);
              }
              b.out.classList.remove("is-stale");
              return;
            }
            var res = dang.literateEval(sessionId, source);
            renderReplOutput(b.out, res);
            b.out.classList.remove("is-stale");
            if (!res.ok) failed = true;
          });
        })
        .catch(function (err) {
          var out = origin ? origin.out : chain[0].out;
          renderReplOutput(out, {
            ok: false, stage: "", value: "", output: "",
            error: "Failed to load: " + (err && err.message ? err.message : err),
          });
        })
        .then(function () {
          running = false;
          chain.forEach(function (b) {
            b.runBtn.disabled = false;
            b.runBtn.textContent = "Run ▶";
          });
        });
    }

    function build(container, index) {
      var fallback = container.querySelector(".dang-literate-fallback");
      if (!fallback) return;
      var seed = fallback.cloneNode(true);
      seed.querySelectorAll(".dang-fb").forEach(function (n) { n.remove(); });
      var source = seed.textContent.replace(/\s+$/, "");
      var seedCode = seed.querySelector("code");
      var seedHtml = seedCode ? seedCode.innerHTML : null;

      // Reuse the baked output element so the build-time results stay on
      // screen until the first replay; create one for output-less blocks.
      var out = container.querySelector(".dang-literate-out");
      if (!out) {
        out = document.createElement("div");
        out.className = "dang-literate-out is-empty";
        container.appendChild(out);
      }

      // Same overlay editor as the playground: transparent textarea over a
      // highlighted layer with identical metrics.
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
      // rows=1: a textarea defaults to two rows, and autosize can't shrink
      // below that (scrollHeight includes the rows-derived minimum), which
      // would render every single-line block two lines tall.
      editor.setAttribute("rows", "1");
      editor.value = source;
      editorWrap.appendChild(highlight);
      editorWrap.appendChild(editor);
      container.replaceChild(editorWrap, fallback);

      var controls = document.createElement("div");
      controls.className = "dang-literate-controls";
      var runBtn = document.createElement("button");
      runBtn.className = "dang-playground-btn dang-playground-run";
      runBtn.type = "button";
      runBtn.textContent = "Run ▶";
      runBtn.title = "Replay every block on this page in one shared environment";
      controls.appendChild(runBtn);
      container.appendChild(controls);

      var entry = {
        editor: editor,
        out: out,
        runBtn: runBtn,
        expectFailure: container.hasAttribute("data-dang-literate-failure"),
      };
      runBtn.addEventListener("click", function () { runChain(entry); });

      function rehighlight() {
        highlight.innerHTML = highlightHtml(editor.value);
      }
      function autosize() {
        editor.style.height = "auto";
        editor.style.height = editor.scrollHeight + "px";
      }
      autosizers.push(autosize);
      if (seedHtml && !ts) {
        highlight.innerHTML = seedHtml;
      } else {
        rehighlight();
      }
      autosize();
      editor.addEventListener("input", function () {
        rehighlight();
        autosize();
        // This output and everything downstream may no longer match the code.
        for (var i = index; i < chain.length; i++) {
          chain[i].out.classList.add("is-stale");
        }
      });
      loadTreeSitter().then(function () { rehighlight(); });

      editor.addEventListener("keydown", function (e) {
        if (e.key === "Tab") {
          e.preventDefault();
          var s = editor.selectionStart, en = editor.selectionEnd;
          editor.value = editor.value.slice(0, s) + "  " + editor.value.slice(en);
          editor.selectionStart = editor.selectionEnd = s + 2;
          rehighlight();
          autosize();
        }
        if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
          e.preventDefault();
          runChain(entry);
        }
      });

      chain.push(entry);
    }

    for (var i = 0; i < containers.length; i++) build(containers[i], chain.length);
  }

  function init() {
    var blocks = document.querySelectorAll("[data-dang-playground]");
    for (var i = 0; i < blocks.length; i++) enhance(blocks[i]);
    var repls = document.querySelectorAll("[data-dang-repl]");
    for (var j = 0; j < repls.length; j++) enhanceRepl(repls[j]);
    var lazies = document.querySelectorAll("[data-dang-repl-lazy]");
    for (var k = 0; k < lazies.length; k++) enhanceLazyRepl(lazies[k]);
    var lits = document.querySelectorAll("[data-dang-literate]");
    if (lits.length) enhanceLiterateChain(lits);
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
