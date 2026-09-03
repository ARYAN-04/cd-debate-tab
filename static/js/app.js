function getCsrfToken() {
  var meta = document.querySelector('meta[name="csrf-token"]');
  if (meta && meta.content) return meta.content;
  var m = document.cookie.match(/(?:^|;\s*)csrf=([^;]+)/);
  return m ? decodeURIComponent(m[1]) : "";
}

// SortableJS wiring: drag team cards between rooms, POST team_id+target_room_id,
// swap #draft-canvas with the returned fragment. Fragment swaps replace DOM
// nodes, so Sortable must be (re-)attached after every swap.
function initSortable(root) {
  if (typeof Sortable === "undefined") return;
  var status = document.body.getAttribute("data-round-status");
  if (status && status !== "draft") return;
  root.querySelectorAll(".sortable-list").forEach(function (el) {
    if (el.dataset.sortableOn) return;
    el.dataset.sortableOn = "1";
    Sortable.create(el, {
      group: "rooms",
      animation: 150,
      emptyInsertThreshold: 12,
      fallbackTolerance: 3,
      ghostClass: "sortable-ghost",
      onAdd: function (evt) {
        var card = evt.item;
        var teamId = card.getAttribute("data-team-id");
        var targetRoomId = evt.to.getAttribute("data-room-id");
        var roundId = document.body.getAttribute("data-round-id");
        var params = new URLSearchParams({ team_id: teamId, target_room_id: targetRoomId });
        fetch("/admin/rounds/" + roundId + "/move-team", {
          method: "POST",
          headers: {
            "Content-Type": "application/x-www-form-urlencoded",
            "X-CSRF-Token": getCsrfToken(),
          },
          body: params.toString(),
        })
          .then(function (r) {
            if (!r.ok) throw new Error("move failed");
            return r.text();
          })
          .then(function (html) {
            var tmp = document.createElement("div");
            tmp.innerHTML = html;
            var next = tmp.querySelector("#draft-canvas");
            if (next) {
              document.querySelector("#draft-canvas").replaceWith(next);
              // Native DOM insertion bypasses HTMX: process new Flip-side
              // buttons manually, then re-attach drag-drop.
              if (typeof htmx !== "undefined") htmx.process(next);
              initSortable(document);
            }
          })
          .catch(function () {
            window.location.reload();
          });
      },
    });
  });
}

document.addEventListener("DOMContentLoaded", function () {
  initSortable(document);
});
// HTMX fragment swaps (e.g. Flip sides) replace the lists: re-attach.
document.addEventListener("htmx:afterSwap", function (e) {
  initSortable(e.target);
});

// Attach CSRF header to every HTMX request.
document.addEventListener("htmx:configRequest", function (evt) {
  var tok = getCsrfToken();
  if (tok) evt.detail.headers["X-CSRF-Token"] = tok;
});

// Inject csrf_token into standard POST forms if missing.
document.addEventListener("submit", function (e) {
  var form = e.target;
  if (!form || !form.method || form.method.toLowerCase() !== "post") return;
  if (form.querySelector("input[name='csrf_token']")) return;
  var tok = getCsrfToken();
  if (!tok) return;
  var inp = document.createElement("input");
  inp.type = "hidden";
  inp.name = "csrf_token";
  inp.value = tok;
  form.appendChild(inp);
});
