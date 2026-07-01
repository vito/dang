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
// tabs where the active tab is left-aligned as the slide's header and the rest
// trail off to the right, fading into a filled prev/next segment at the bar's
// right edge. Click a tab to jump to it; the arrows (or ←/→) step through.
// Each slide is a #slug link target, so the URL fragment focuses one on load
// and the active slide is reflected back into the fragment as you navigate.
// The snippets are baked at build time (docs/go/carousel.go), so this is pure
// presentation.

(function () {
  "use strict";

  // Turn a feature title into a URL-fragment slug, so a slide is deep-linkable
  // (#records) and the fragment focuses it on load.
  function slugify(s) {
    return s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  }

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

    var slugs = [];
    var tabs = slides.map(function (slide, i) {
      var titleEl = slide.querySelector(".dang-carousel-title");
      var label = titleEl ? titleEl.textContent.trim() : "Example " + (i + 1);
      // Each slide is a link target (#slug), so it can be focused from the URL.
      var slug = slugify(label) || "slide-" + (i + 1);
      slide.id = slug;
      slugs.push(slug);
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

    // Prev/next arrows as a filled segment on the right, over a fade so the
    // trailing tabs dissolve into it instead of colliding.
    var nav = document.createElement("div");
    nav.className = "dang-carousel-nav";
    var prev = document.createElement("button");
    prev.className = "dang-carousel-arrow";
    prev.type = "button";
    prev.setAttribute("aria-label", "Previous feature");
    prev.innerHTML = "&#8249;"; // ‹
    prev.addEventListener("click", function () { show(active - 1); });
    var next = document.createElement("button");
    next.className = "dang-carousel-arrow";
    next.type = "button";
    next.setAttribute("aria-label", "Next feature");
    next.innerHTML = "&#8250;"; // ›
    next.addEventListener("click", function () { show(active + 1); });
    nav.appendChild(prev);
    nav.appendChild(next);
    bar.appendChild(nav);

    carousel.insertBefore(bar, carousel.firstChild);
    carousel.classList.add("is-enhanced");

    // Slide the track so the active tab is left-aligned as the header (where the
    // first tab sits); the rest trail off to the right. offsetLeft is a layout
    // position, so it ignores the animating transform and stays correct mid-slide.
    function reposition() {
      if (active < 0) return;
      track.style.transform =
        "translateX(" + (tabs[0].offsetLeft - tabs[active].offsetLeft) + "px)";
    }

    function show(i, updateHash) {
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
      // Reflect the active slide in the URL fragment (replaceState so it stays
      // shareable without spamming history or scrolling). Skipped for the
      // initial focus and for changes that came from the fragment itself.
      if (updateHash !== false && slugs[i]) {
        try { history.replaceState(null, "", "#" + slugs[i]); } catch (e) { /* ignore */ }
      }
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

    // Deep-linking: focus the slide named by the URL fragment (on load and on
    // later #hash changes, e.g. back/forward or an in-page link).
    function slideFromHash() {
      return slugs.indexOf(decodeURIComponent((location.hash || "").replace(/^#/, "")));
    }
    window.addEventListener("hashchange", function () {
      var i = slideFromHash();
      if (i >= 0) show(i, false);
    });
    var start = slideFromHash();
    show(start >= 0 ? start : 0, false); // don't rewrite the fragment on load
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
