// Gameday theme toggle.
//
// Flips Bulma's dark mode by setting data-theme on the <html> element
// (see https://bulma.io/documentation/features/dark-mode/). The
// attribute is always set to "light" or "dark" (never removed) so the
// choice overrides the system's prefers-color-scheme in both
// directions. The choice is persisted in localStorage and applied
// before first paint to avoid a flash of the wrong theme.
//
// Portable: no framework, no build step, no external assets. The only
// DOM contract is an element with id "theme-toggle" (a checkbox input);
// its checked state is the source of truth for the theme.

(function () {
  "use strict";

  var STORAGE_KEY = "gameday-theme";

  function applyTheme(dark) {
    document.documentElement.setAttribute("data-theme", dark ? "dark" : "light");
  }

  function storedTheme() {
    try {
      return localStorage.getItem(STORAGE_KEY) === "dark";
    } catch (err) {
      return false;
    }
  }

  // Apply the persisted theme as early as possible.
  applyTheme(storedTheme());

  function init() {
    var toggle = document.getElementById("theme-toggle");
    if (!toggle) {
      return;
    }

    toggle.checked = storedTheme();

    toggle.addEventListener("change", function () {
      applyTheme(toggle.checked);
      try {
        localStorage.setItem(STORAGE_KEY, toggle.checked ? "dark" : "light");
      } catch (err) {
        // Storage unavailable (private mode, etc.); the theme still
        // applies for the current page.
      }
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
