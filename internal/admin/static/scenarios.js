// Scenario pages of theserver admin (D-036): uploads a package with a
// progress bar, and asks before a draft is deleted. The pages work without
// this script; the upload then posts as a plain form.
(function () {
  "use strict";

  document.querySelectorAll("form[data-confirm]").forEach(function (form) {
    form.addEventListener("submit", function (event) {
      if (!window.confirm(form.dataset.confirm)) {
        event.preventDefault();
      }
    });
  });

  var form = document.getElementById("upload-form");
  if (!form || !window.XMLHttpRequest || !window.FormData || !window.DOMParser) {
    return;
  }
  var bar = document.getElementById("upload-progress");
  var status = document.getElementById("upload-status");
  var button = document.getElementById("upload-button");

  function show(percent) {
    bar.value = percent;
    status.textContent = form.dataset.progress.replace("%d", String(percent));
  }

  function finish(text) {
    button.disabled = false;
    bar.hidden = true;
    status.textContent = text;
  }

  form.addEventListener("submit", function (event) {
    event.preventDefault();
    var xhr = new XMLHttpRequest();
    xhr.open("POST", form.action);
    xhr.upload.addEventListener("progress", function (e) {
      if (e.lengthComputable) {
        show(Math.round((100 * e.loaded) / e.total));
      }
    });
    xhr.addEventListener("load", function () {
      // A stored draft answers with a redirect to its page, which the
      // request followed.
      if (xhr.responseURL && xhr.responseURL !== form.action) {
        window.location.assign(xhr.responseURL);
        return;
      }
      var fresh = new DOMParser().parseFromString(xhr.responseText, "text/html").getElementById("catalogue");
      var current = document.getElementById("catalogue");
      if (fresh && current) {
        current.replaceWith(fresh);
        finish("");
        return;
      }
      finish(form.dataset.failed);
    });
    xhr.addEventListener("error", function () {
      finish(form.dataset.failed);
    });
    button.disabled = true;
    bar.hidden = false;
    show(0);
    xhr.send(new FormData(form));
  });
})();
