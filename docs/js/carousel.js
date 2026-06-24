// carousel.js — progressive enhancement for \dang-carousel blocks.
//
// Each carousel is authored as a stack of slides:
//
//   <div class="dang-carousel" data-dang-carousel>
//     <div class="dang-carousel-slide" data-dang-carousel-slide>
//       <div class="dang-carousel-title">Prototype objects</div>
//       <div class="dang-carousel-code">…baked snippet + output…</div>
//     </div>
//     …more slides…
//   </div>
//
// Without JS the reader sees every slide in sequence — a plain list of
// examples. With JS we add a tab strip of feature names (jump to any slide), a
// prev/counter/next footer, and arrow-key navigation, and show one slide at a
// time. The snippets are baked at build time (docs/go/carousel.go), so nothing
// here loads or evaluates Dang — this is pure presentation.

(function () {
  "use strict";

  function enhance(carousel) {
    var slides = Array.prototype.slice.call(
      carousel.querySelectorAll(":scope > [data-dang-carousel-slide]")
    );
    // A lone slide isn't a carousel; leave it as the plain block it already is.
    if (slides.length < 2) return;

    var active = -1;

    // Tab strip: one tab per slide, labelled by the slide's feature title.
    var tabs = document.createElement("div");
    tabs.className = "dang-carousel-tabs";
    tabs.setAttribute("role", "tablist");
    var tabBtns = slides.map(function (slide, i) {
      var titleEl = slide.querySelector(".dang-carousel-title");
      var label = titleEl ? titleEl.textContent.trim() : "Example " + (i + 1);
      var tab = document.createElement("button");
      tab.className = "dang-carousel-tab";
      tab.type = "button";
      tab.textContent = label;
      tab.setAttribute("role", "tab");
      slide.setAttribute("role", "tabpanel");
      tab.addEventListener("click", function () { show(i); });
      tabs.appendChild(tab);
      return tab;
    });

    // Footer: prev arrow, "n / total" counter, next arrow.
    var foot = document.createElement("div");
    foot.className = "dang-carousel-foot";
    var prev = document.createElement("button");
    prev.className = "dang-carousel-arrow";
    prev.type = "button";
    prev.setAttribute("aria-label", "Previous feature");
    prev.innerHTML = "&#8249;"; // ‹
    var count = document.createElement("span");
    count.className = "dang-carousel-count";
    var next = document.createElement("button");
    next.className = "dang-carousel-arrow";
    next.type = "button";
    next.setAttribute("aria-label", "Next feature");
    next.innerHTML = "&#8250;"; // ›
    foot.appendChild(prev);
    foot.appendChild(count);
    foot.appendChild(next);
    prev.addEventListener("click", function () { show(active - 1); });
    next.addEventListener("click", function () { show(active + 1); });

    carousel.insertBefore(tabs, carousel.firstChild);
    carousel.appendChild(foot);
    carousel.classList.add("is-enhanced");

    function show(i) {
      var n = slides.length;
      i = ((i % n) + n) % n; // wrap around at both ends
      if (i === active) return;
      active = i;
      slides.forEach(function (s, j) {
        var on = j === i;
        s.classList.toggle("is-active", on);
        s.setAttribute("aria-hidden", on ? "false" : "true");
      });
      tabBtns.forEach(function (t, j) {
        var on = j === i;
        t.classList.toggle("is-active", on);
        t.setAttribute("aria-selected", on ? "true" : "false");
        t.tabIndex = on ? 0 : -1;
      });
      count.textContent = (i + 1) + " / " + n;
      // The revealed slide embeds a \dang-literate editor whose textarea
      // autosizes by measuring scrollHeight — which reads 0 while the slide is
      // display:none. playground.js re-runs every editor's autosize on window
      // resize, so nudging it here corrects the just-shown slide's height.
      window.dispatchEvent(new Event("resize"));
    }

    // Left/right arrow keys page through — but not while editing a slide's
    // code (the embedded editor needs the arrows to move the caret).
    carousel.addEventListener("keydown", function (e) {
      if (e.target && e.target.closest("textarea, input")) return;
      if (e.key === "ArrowLeft") { e.preventDefault(); show(active - 1); }
      else if (e.key === "ArrowRight") { e.preventDefault(); show(active + 1); }
    });

    show(0);
  }

  function init() {
    var carousels = document.querySelectorAll("[data-dang-carousel]");
    for (var i = 0; i < carousels.length; i++) enhance(carousels[i]);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
