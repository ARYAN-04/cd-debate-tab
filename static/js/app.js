// SortableJS wiring: drag team cards between rooms, POST team_id+target_room_id,
// swap #draft-canvas with the returned fragment.
document.querySelectorAll(".sortable-list").forEach(function (el) {
  if (typeof Sortable === "undefined") return;
  Sortable.create(el, {
    group: "rooms",
    animation: 150,
    onAdd: function (evt) {
      var card = evt.item;
      var teamId = card.getAttribute("data-team-id");
      var targetRoomId = evt.to.getAttribute("data-room-id");
      var roundId = document.body.getAttribute("data-round-id");
      var params = new URLSearchParams({ team_id: teamId, target_room_id: targetRoomId });
      fetch("/admin/rounds/" + roundId + "/move-team", {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: params.toString(),
      })
        .then(function (r) { return r.text(); })
        .then(function (html) {
          var tmp = document.createElement("div");
          tmp.innerHTML = html;
          var next = tmp.querySelector("#draft-canvas");
          if (next) document.querySelector("#draft-canvas").replaceWith(next);
        });
    },
  });
});
