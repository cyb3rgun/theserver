// Settings page of theserver admin (D-034): counts the fields that differ from
// the saved values, marks them, and asks before a reset drops unsaved
// changes. The page works without this script; it only adds the count.
(function () {
  "use strict";
  var form = document.getElementById("settings-form");
  if (!form) {
    return;
  }
  var counter = document.getElementById("unsaved-count");

  function current(el) {
    return el.type === "checkbox" ? String(el.checked) : el.value;
  }

  function count() {
    var changed = 0;
    form.querySelectorAll("[data-initial]").forEach(function (el) {
      var differs = !el.disabled && current(el) !== el.dataset.initial;
      el.closest(".field").classList.toggle("changed", differs);
      if (differs) {
        changed++;
      }
    });
    var text = changed === 1 ? counter.dataset.singular : counter.dataset.plural;
    counter.textContent = text.replace("%d", String(changed));
    form.classList.toggle("dirty", changed > 0);
    return changed;
  }

  form.addEventListener("input", count);
  form.addEventListener("change", count);
  form.addEventListener("reset", function () {
    window.setTimeout(count, 0);
  });
  form.addEventListener("submit", function (event) {
    var submitter = event.submitter;
    if (submitter && submitter.name === "reset" && count() > 0 && !window.confirm(form.dataset.confirmReset)) {
      event.preventDefault();
    }
  });
  count();
})();
