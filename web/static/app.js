(function () {
  document.documentElement.classList.add("js");
  var input = document.getElementById("finder-input");
  if (!input) return;
  var cards = Array.prototype.slice.call(document.querySelectorAll("[data-filter-item]"));
  var blocks = Array.prototype.slice.call(document.querySelectorAll("[data-filter-block]"));
  var empty = document.getElementById("no-match");

  function apply() {
    var q = input.value.trim().toLowerCase();
    var shown = 0;
    cards.forEach(function (c) {
      var hit = !q || c.getAttribute("data-filter-item").toLowerCase().indexOf(q) !== -1;
      c.hidden = !hit;
      if (hit) shown++;
    });
    blocks.forEach(function (b) {
      b.hidden = !b.querySelector("[data-filter-item]:not([hidden])");
    });
    if (empty) empty.classList.toggle("show", shown === 0);
  }

  input.addEventListener("input", apply);
  document.addEventListener("keydown", function (e) {
    var tag = (e.target.tagName || "").toLowerCase();
    if (e.key === "/" && tag !== "input" && tag !== "textarea" && tag !== "select") {
      e.preventDefault();
      input.focus();
    } else if (e.key === "Escape" && document.activeElement === input) {
      input.value = "";
      apply();
      input.blur();
    }
  });
})();
