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
      s.src = "js/wasm_exec.js";
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
      fetch("js/dang.wasm")
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
    var source = fallback.textContent.replace(/\s+$/, "");
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

    // Editor.
    var editor = document.createElement("textarea");
    editor.className = "dang-playground-input";
    editor.spellcheck = false;
    editor.setAttribute("autocomplete", "off");
    editor.setAttribute("autocapitalize", "off");
    editor.setAttribute("autocorrect", "off");
    editor.value = source;

    // Output.
    var output = document.createElement("div");
    output.className = "dang-playground-output is-empty";
    output.textContent = "";

    container.appendChild(bar);
    container.appendChild(editor);
    container.appendChild(output);

    function autosize() {
      editor.style.height = "auto";
      editor.style.height = editor.scrollHeight + "px";
    }
    autosize();
    editor.addEventListener("input", autosize);

    // Tab inserts two spaces instead of moving focus.
    editor.addEventListener("keydown", function (e) {
      if (e.key === "Tab") {
        e.preventDefault();
        var s = editor.selectionStart, en = editor.selectionEnd;
        editor.value = editor.value.slice(0, s) + "  " + editor.value.slice(en);
        editor.selectionStart = editor.selectionEnd = s + 2;
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
