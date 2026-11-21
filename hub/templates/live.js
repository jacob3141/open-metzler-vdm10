// Live view: plays /admin/live (fragmented MP4) with Media Source Extensions.
"use strict";

(() => {
  const video = document.getElementById("live");
  const button = document.getElementById("live-toggle");
  const state = document.getElementById("live-state");
  const MS = window.ManagedMediaSource || window.MediaSource;
  const maxLag = 1.5;   // seconds behind the newest frame before jumping ahead
  const keepBack = 10;  // seconds of played video kept in the buffer

  let wanted = false;
  let abort = null;
  let retry = null;

  function setState(s) { state.textContent = s; }

  function setButton() {
    button.textContent = wanted ? "Stop live view" : "Start live view";
    button.classList.toggle("primary", !wanted);
  }

  function detach() {
    video.pause();
    video.removeAttribute("src");
    video.srcObject = null;
    video.load();
  }

  async function play() {
    abort = new AbortController();
    setState("Connecting …");
    // location.origin drops user:password@ from the page URL, which fetch would refuse.
    const res = await fetch(location.origin + "/admin/live", { signal: abort.signal, cache: "no-store" });
    if (!res.ok) throw new Error((await res.text()).trim() || res.statusText);
    const type = 'video/mp4; codecs="' + res.headers.get("X-Video-Codec") + '"';
    if (!MS.isTypeSupported(type)) throw new Error("This browser cannot play " + type);

    const ms = new MS();
    video.disableRemotePlayback = true;
    if (window.ManagedMediaSource && MS === window.ManagedMediaSource) {
      video.srcObject = ms;
    } else {
      video.src = URL.createObjectURL(ms);
    }
    await new Promise((ok) => ms.addEventListener("sourceopen", ok, { once: true }));
    const sb = ms.addSourceBuffer(type);
    sb.mode = "segments";

    const queue = [];
    const pump = () => {
      if (sb.updating || ms.readyState !== "open") return;
      const b = sb.buffered;
      if (b.length) {
        const end = b.end(b.length - 1);
        if (end - video.currentTime > maxLag) video.currentTime = end - 0.2;
        if (video.currentTime - b.start(0) > 2 * keepBack) {
          sb.remove(b.start(0), video.currentTime - keepBack);
          return;
        }
      }
      if (!queue.length) return;
      const size = queue.reduce((n, c) => n + c.length, 0);
      const chunk = new Uint8Array(size);
      let at = 0;
      for (const c of queue) { chunk.set(c, at); at += c.length; }
      queue.length = 0;
      try {
        sb.appendBuffer(chunk);
      } catch (e) {
        if (e.name !== "QuotaExceededError") throw e;
      }
    };
    sb.addEventListener("updateend", () => {
      if (video.paused && sb.buffered.length) {
        video.play().then(() => setState("Live")).catch(() => setState("Press play to start"));
      }
      pump();
    });

    const reader = res.body.getReader();
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      queue.push(value);
      pump();
    }
    throw new Error("The stream ended");
  }

  async function run() {
    clearTimeout(retry);
    try {
      await play();
    } catch (e) {
      if (!wanted) return;
      setState(e.message + " – retrying …");
      detach();
      retry = setTimeout(run, 3000);
    }
  }

  function start() {
    wanted = true;
    setButton();
    video.hidden = false;
    run();
  }

  function stop() {
    wanted = false;
    clearTimeout(retry);
    if (abort) abort.abort();
    detach();
    video.hidden = true;
    setButton();
    setState("");
  }

  if (!MS) {
    button.disabled = true;
    setState("This browser does not support Media Source Extensions.");
    return;
  }
  video.muted = true;
  button.addEventListener("click", () => (wanted ? stop() : start()));
  document.addEventListener("visibilitychange", () => {
    if (document.hidden && wanted) stop();
  });
  setButton();
})();
