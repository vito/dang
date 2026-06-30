// carousel.js — progressive enhancement for \dang-carousel blocks.
//
// Each slide (\dang-feature / \dang-github-feature) renders as a titled block:
//
//   <div class="dang-carousel" data-dang-carousel>
//     <div class="dang-carousel-slide" data-dang-carousel-slide>
//       <div class="dang-carousel-title">Schema-native types</div>
//       …code block (+ optional prose notes)…
//     </div>
//     …more slides…
//   </div>
//
// Without JS every slide shows in sequence — a plain list of examples. With JS
// we show one at a time behind a single top bar: a sliding track of feature
// tabs where the active tab is pinned to the LEFT as the slide's header and the
// rest trail off to the right, fading into the prev/next arrows at the bar's
// right edge. Click a tab to jump to it; the arrows (or ←/→) step through. The
// snippets are baked at build time (docs/go/carousel.go), so this is pure
// presentation.

(function () {
  "use strict";

  function enhance(carousel) {
    var slides = Array.prototype.slice.call(
      carousel.querySelectorAll(":scope > [data-dang-carousel-slide]")
    );
    // A lone slide isn't a carousel; leave it as the plain block it already is.
    if (slides.length < 2) return;

    var active = -1;

    // Single bar holding a sliding track of tabs.
    var bar = document.createElement("div");
    bar.className = "dang-carousel-tabs";
    bar.setAttribute("role", "tablist");
    var track = document.createElement("div");
    track.className = "dang-carousel-track";
    bar.appendChild(track);

    var tabs = slides.map(function (slide, i) {
      var titleEl = slide.querySelector(".dang-carousel-title");
      var label = titleEl ? titleEl.textContent.trim() : "Example " + (i + 1);
      var tab = document.createElement("button");
      tab.className = "dang-carousel-tab";
      tab.type = "button";
      tab.textContent = label;
      tab.setAttribute("role", "tab");
      slide.setAttribute("role", "tabpanel");
      tab.addEventListener("click", function () { show(i); });
      track.appendChild(tab);
      return tab;
    });

    // Prev/next arrows pinned to the right of the bar (over the fade).
    var nav = document.createElement("div");
    nav.className = "dang-carousel-nav";
    var prev = document.createElement("button");
    prev.className = "dang-carousel-arrow";
    prev.type = "button";
    prev.setAttribute("aria-label", "Previous feature");
    prev.innerHTML = "&#8249;"; // ‹
    var next = document.createElement("button");
    next.className = "dang-carousel-arrow";
    next.type = "button";
    next.setAttribute("aria-label", "Next feature");
    next.innerHTML = "&#8250;"; // ›
    prev.addEventListener("click", function () { show(active - 1); });
    next.addEventListener("click", function () { show(active + 1); });
    nav.appendChild(prev);
    nav.appendChild(next);
    bar.appendChild(nav);

    carousel.insertBefore(bar, carousel.firstChild);
    carousel.classList.add("is-enhanced");

    // Slide the track so the active tab's left edge lands where the first tab
    // sits — the bar's left header slot. offsetLeft is a layout position, so it
    // ignores the (animating) transform and stays correct mid-slide.
    function reposition() {
      if (active < 0) return;
      track.style.transform =
        "translateX(" + (tabs[0].offsetLeft - tabs[active].offsetLeft) + "px)";
    }

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
      tabs.forEach(function (t, j) {
        var on = j === i;
        t.classList.toggle("is-active", on);
        t.setAttribute("aria-selected", on ? "true" : "false");
        t.tabIndex = on ? 0 : -1;
      });
      reposition();
      // The revealed slide may hold an editor whose textarea autosizes by
      // measuring scrollHeight — which reads 0 while display:none. playground.js
      // re-runs every autosize on window resize, so nudge it.
      window.dispatchEvent(new Event("resize"));
    }

    // Left/right arrow keys page through, when a tab has focus.
    bar.addEventListener("keydown", function (e) {
      if (e.key === "ArrowLeft") { e.preventDefault(); show(active - 1); }
      else if (e.key === "ArrowRight") { e.preventDefault(); show(active + 1); }
    });

    // Tab metrics change with the viewport and once the webfont swaps in, so
    // re-align then. (reposition reads offsets fresh, so this just re-snaps.)
    window.addEventListener("resize", reposition);
    if (document.fonts && document.fonts.ready) document.fonts.ready.then(reposition);

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
