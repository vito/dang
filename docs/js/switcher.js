// Base16 theme switcher, ported from vito/bass's docs.
//
// The whole site is colored through --base00..--base0F variables (see
// chroma.css and page.tmpl); each stylesheet under css/base16/ defines one
// scheme's variables. This script applies the active scheme, persists an
// explicit choice, and — until the reader picks one — rolls a random curated
// scheme once per browser session from the list matching the OS light/dark
// preference, following it live if it changes.
//
// Loaded early (no defer): the active scheme's stylesheet is written
// synchronously while the head is still parsing, so the page first-paints in
// the right colors with no flash of the chroma.css fallback palette between
// page navigations. The <select id="styleswitcher"> in the sidebar doesn't
// exist yet at that point, so UI sync happens on window.onload.

const styleKey = "dang-code-theme";
const rollKey = "dang-code-theme-roll";
const linkId = "theme";

const controlsId = "choosetheme";
const switcherId = "styleswitcher";
const resetId = "resetstyle";

function storeStyle(style) {
  window.localStorage.setItem(styleKey, style);
}

function loadStyle() {
  return window.localStorage.getItem(styleKey);
}

function styleHref(style) {
  return "css/base16/base16-" + style + ".css";
}

// setActiveStyle swaps the scheme at runtime (select changes, OS preference
// changes). Initial page load doesn't come through here — see the
// document.write at the bottom.
function setActiveStyle(style) {
  var link = document.createElement("link");
  link.rel = "stylesheet";
  link.type = "text/css";
  link.href = styleHref(style);
  link.media = "all";

  // only swap the element once the CSS has loaded to prevent flickering
  link.onload = function () {
    var prevLink = document.getElementById(linkId);
    if (prevLink) {
      prevLink.remove();
    }

    link.id = linkId;
  };

  document.head.appendChild(link);

  syncControls(style);
}

function syncControls(style) {
  var switcher = document.getElementById(switcherId);
  if (switcher) {
    switcher.value = style;
  }

  resetReset();
}

function resetReset() {
  var style = loadStyle();
  var reset = document.getElementById(resetId);
  if (!style) {
    if (reset) {
      // no style selected; remove reset element
      reset.remove();
    }

    return;
  }

  if (reset) {
    // has style and reset; done
    return;
  }

  // has style but no reset element
  reset = document.createElement("a");
  reset.id = resetId;
  reset.onclick = resetStyle;
  reset.href = "javascript:void(0)";
  reset.text = "reset";
  reset.className = "reset";

  var chooser = document.getElementById(controlsId);
  if (chooser) {
    chooser.appendChild(reset);
  }
}

function switchStyle(event) {
  var style = event.target.value;
  storeStyle(style);
  activeStyle = style;
  setActiveStyle(style);
}

function resetStyle() {
  window.localStorage.removeItem(styleKey);
  activeStyle = rollStyle();
  setActiveStyle(activeStyle);
}

// Curated schemes must read well under page.tmpl's role mapping: base00 is
// the page background, base04 colors most prose, base01/base02 are secondary
// *backgrounds* (sidebar, selection, inline code), and base08-0F are syntax
// foregrounds on base00. Schemes that repurpose base01/base02 as saturated
// accents (papercolor-light, edge-light) or have washed-out base04/base0A
// render the docs illegibly, so they stay out of the rotation even though
// they remain selectable in the dropdown.
var curatedDarkStyles = [
  "chalk",
  "classic-dark",
  "darkmoss",
  "decaf",
  "default-dark",
  "dracula",
  "eighties",
  "equilibrium-dark",
  "equilibrium-gray-dark",
  "espresso",
  "framer",
  "gruvbox-dark-medium",
  "hardcore",
  "horizon-dark",
  "horizon-terminal-dark",
  "ir-black",
  "materia",
  "material",
  "mocha",
  "monokai",
  "ocean",
  "oceanicnext",
  "outrun-dark",
  "rose-pine",
  "rose-pine-moon",
  "snazzy",
  "tender",
  "tokyo-night-dark",
  "tomorrow-night",
  "tomorrow-night-eighties",
  "twilight",
  "woodland",
];

var curatedLightStyles = [
  "cupertino",
  "equilibrium-gray-light",
  "equilibrium-light",
  "gruvbox-light-medium",
  "ia-light",
  "one-light",
  "rose-pine-dawn",
  "tokyo-night-light",
];

var prefersLight = window.matchMedia("(prefers-color-scheme: light)");

function curatedStyles() {
  return prefersLight.matches ? curatedLightStyles : curatedDarkStyles;
}

// rollStyle picks a fresh random scheme for the current OS mode and remembers
// it for the rest of the browser session, so navigating between pages keeps
// one scheme instead of re-rolling on every load.
function rollStyle() {
  var styles = curatedStyles();
  var style = styles[Math.floor(Math.random() * styles.length)];
  window.sessionStorage.setItem(rollKey, style);
  return style;
}

// initialStyle resolves what this page load should paint with: an explicit
// choice, else this session's roll (re-rolled if the OS mode no longer
// matches it), else a fresh roll.
function initialStyle() {
  var stored = loadStyle();
  if (stored) {
    return stored;
  }

  var rolled = window.sessionStorage.getItem(rollKey);
  if (rolled && curatedStyles().indexOf(rolled) !== -1) {
    return rolled;
  }

  return rollStyle();
}

var activeStyle;
try {
  activeStyle = initialStyle();
} catch (e) {
  // storage can throw in some privacy modes; still paint something
  activeStyle = curatedStyles()[0];
}

// Apply the scheme as a parser-inserted stylesheet: document.write during
// head parsing makes it render-blocking, so the first paint already has the
// right palette — no flash of the fallback :root in chroma.css. This relies
// on the script being a plain (non-deferred) <script> in <head>.
document.write('<link id="' + linkId + '" rel="stylesheet" type="text/css" href="' + styleHref(activeStyle) + '">');

// preload the curated styles for the active mode so switching is instant
curatedStyles().forEach(function (style) {
  var link = document.createElement("link");
  link.rel = "alternate stylesheet";
  link.title = style;
  link.type = "text/css";
  link.href = styleHref(style);
  link.media = "all";
  document.head.appendChild(link);
});

// follow OS light/dark changes until the reader picks a scheme themselves
prefersLight.addEventListener("change", function () {
  if (loadStyle()) {
    return;
  }
  activeStyle = rollStyle();
  setActiveStyle(activeStyle);
});

window.addEventListener("load", function () {
  // the sidebar controls exist now; reflect the active scheme in them
  syncControls(activeStyle);
});
