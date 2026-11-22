// Shows devices that ask for access (POST /api/register) without reloading the page.
"use strict";

(() => {
  const section = document.getElementById("requests");
  const list = document.getElementById("request-list");
  let shown = Array.from(list.children, (r) => r.dataset.id).join(",");

  function el(tag, props, ...children) {
    const e = Object.assign(document.createElement(tag), props);
    e.append(...children);
    return e;
  }

  function form(id, action, label, primary) {
    const b = el("button", { type: "submit", textContent: label });
    if (primary) b.className = "primary";
    return el("form", { className: "inline", method: "post", action: "/admin/registrations/" + encodeURIComponent(id) + "/" + action }, b);
  }

  function render(reqs) {
    const ids = reqs.map((r) => r.id).join(",");
    if (ids === shown) return;
    shown = ids;
    list.replaceChildren(...reqs.map((r) => {
      const row = el("div", { className: "row" },
        el("div", {}, el("strong", { textContent: r.name }), " ", el("span", { className: "muted", textContent: "Code" }), " ", el("span", { className: "code", textContent: r.code })),
        el("div", {}, form(r.id, "deny", "Deny", false), " ", form(r.id, "approve", "Approve", true)));
      row.dataset.id = r.id;
      return row;
    }));
    section.hidden = reqs.length === 0;
  }

  async function poll() {
    if (!document.hidden) {
      try {
        const res = await fetch(location.origin + "/admin/registrations", { cache: "no-store" });
        if (res.ok) render(await res.json());
      } catch (e) {
        // the next round tries again
      }
    }
    setTimeout(poll, 3000);
  }
  setTimeout(poll, 3000);
})();

// shows the value of the volume slider while it moves
(() => {
  const range = document.getElementById("talk_volume");
  const out = range && range.form.querySelector("output");
  if (out) range.addEventListener("input", () => { out.textContent = range.value + " %"; });
})();

// shows the tamper switch live, e.g. while mounting the door station
(() => {
  const section = document.getElementById("mounting");
  const sw = document.getElementById("tamper-switch");
  const place = document.getElementById("tamper-place");
  if (!section) return;
  async function poll() {
    if (!document.hidden) {
      try {
        const res = await fetch(location.origin + "/admin/tamper", { cache: "no-store" });
        if (res.ok) {
          const t = await res.json();
          if (String(t.mount) !== section.dataset.mount) {
            location.reload(); // the buttons change with the mounting mode
            return;
          }
          sw.textContent = t.connected ? (t.pressed ? "pressed" : "free") : "unknown, the door station is not connected";
          sw.className = t.connected && t.pressed ? "ok" : "bad";
          place.textContent = t.connected ? (t.open ? "counts as off the wall" : "counts as on the wall") : "";
        }
      } catch (e) {
        // the next round tries again
      }
    }
    setTimeout(poll, 500);
  }
  setTimeout(poll, 500);
})();
