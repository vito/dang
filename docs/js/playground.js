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
      // main() calls window.onDangReady once dangEval is registered.
      window.onDangReady = function () {
        resolve(window.dangEval);
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

  var STAGE_LABEL = { parse: "Parse error", type: "Type error", eval: "Runtime error" };

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
    container.innerHTML = "";

    // Toolbar.
    var bar = document.createElement("div");
    bar.className = "dang-playground-bar";
    var label = document.createElement("span");
    label.className = "dang-playground-label";
    label.textContent = "Dang · runs in your browser";
    var spacer = document.createElement("span");
    spacer.style.flex = "1";
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
      running = true;
      runBtn.disabled = true;
      runBtn.textContent = "Running…";
      output.className = "dang-playground-output";
      output.textContent = "Compiling…";
      loadDang()
        .then(function (dangEval) {
          output.textContent = "";
          var res = dangEval(editor.value);
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

  function init() {
    var blocks = document.querySelectorAll("[data-dang-playground]");
    for (var i = 0; i < blocks.length; i++) enhance(blocks[i]);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
