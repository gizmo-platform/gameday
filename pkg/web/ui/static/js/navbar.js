// Gameday mobile navbar toggle.
//
// Bulma hides .navbar-menu below 1024px until it has is-active, and
// ships no JavaScript to set it (see
// https://bulma.io/documentation/components/navbar/). Each
// .navbar-burger toggles is-active on itself (drawing a cross) and on
// the menu named by its data-target, and mirrors the state into
// aria-expanded for assistive technology.
//
// Portable: no framework, no build step. Loaded with defer, so the
// DOM is parsed when this runs.

(function () {
  "use strict";

  document.querySelectorAll(".navbar-burger").forEach(function (burger) {
    var menu = document.getElementById(burger.dataset.target);
    if (!menu) {
      return;
    }
    burger.addEventListener("click", function () {
      var open = !menu.classList.contains("is-active");
      menu.classList.toggle("is-active", open);
      burger.classList.toggle("is-active", open);
      burger.setAttribute("aria-expanded", open ? "true" : "false");
    });
  });
})();
