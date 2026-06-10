// Base16 theme switcher for the code surfaces, ported from vito/bass's docs.
//
// Every code surface (static blocks, signature cards, playground/REPL) is
// colored through --base00..--base0F variables (see chroma.css); each
// stylesheet under css/base16/ defines one scheme's variables. This script
// swaps the active scheme stylesheet, persists the choice, and — until the
// reader picks one — rolls a random curated scheme per page load.
//
// Loaded early (no defer) so the scheme applies before first paint; the
// <select id="styleswitcher"> in the sidebar may not exist yet, so UI sync
// happens again on window.onload.

const styleKey = "dang-code-theme";
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

function setActiveStyle(style) {
  var link = document.createElement("link");
  link.rel = "stylesheet";
  link.type = "text/css";
  link.href = "css/base16/base16-" + style + ".css";
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

function setStyleOrDefault(def) {
  setActiveStyle(loadStyle() || def);
}

function switchStyle(event) {
  var style = event.target.value;
  storeStyle(style);
  setActiveStyle(style);
}

function resetStyle() {
  window.localStorage.removeItem(styleKey);
  setActiveStyle(defaultStyle);
}

var curatedStyles = [
  "catppuccin",
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

var defaultStyle = curatedStyles[Math.floor(Math.random() * curatedStyles.length)];

// preload all curated styles to prevent flickering
curatedStyles.forEach(function (style) {
  var link = document.createElement("link");
  link.rel = "alternate stylesheet";
  link.title = style;
  link.type = "text/css";
  link.href = "css/base16/base16-" + style + ".css";
  link.media = "all";
  document.head.appendChild(link);
});

setStyleOrDefault(defaultStyle);

window.addEventListener("load", function () {
  // call again to update switcher selection now that the DOM exists
  setStyleOrDefault(defaultStyle);
});
