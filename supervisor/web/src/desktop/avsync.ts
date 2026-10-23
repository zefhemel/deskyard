// The ?avsync=1 readout: with the A/V test page playing in the desktop
// (/opt/workspace/avsync/index.html?epoch=1 in the image), it measures how
// far the sound is from the picture, and how late each is, and shows them in
// a corner. The numbers are also on window.__avsync for tests.
// - The flash: every animation frame it samples the middle of Selkies'
//   picture (screen.video()) and notes when its mean luminance rises above
//   0.7. Resolution is one display frame.
// - The beep: our additions report each block of sound with the time it
//   reaches the speakers (screen.watchSound); an onset is a block above
//   BEEP_RMS after quiet.
// - Sync offset = beep − flash, both on this browser's clock: positive means
//   the sound is late. It doesn't depend on the desktop's clock.
// - Lag: Selkies' stream has no capture times, so the test page's epoch mode
//   beeps and flashes on whole seconds of the desktop's clock, and lag = when
//   heard or seen here − the nearest whole second. It assumes the clocks are
//   in sync (NTP; the same machine in tests).
import type { DeskyardScreen } from "./selkies.ts";

/** Per paired beep: when it was heard (ms since the epoch), the sync offset, the sound's lag and the picture's. */
export interface AvSyncData {
  at: number[];
  offsets: number[];
  lags: number[];
  vlags: number[];
}

declare global {
  interface Window {
    __avsync?: AvSyncData;
  }
}

const FLASH_LUMA = 0.7;
const BEEP_RMS = 0.1;
const QUIET_MS = 300; // silence before a beep onset
const PAIR_MS = 450; // the pattern repeats every second
const WINDOW = 10; // beeps in the medians

/** Whether the page was opened with ?avsync=1. */
export function avSyncWanted(): boolean {
  return new URLSearchParams(location.search).get("avsync") === "1";
}

/** Starts the readout for the frame's client; returns a function that stops it. */
export function startAvSync(screen: DeskyardScreen): () => void {
  const data: AvSyncData = { at: [], offsets: [], lags: [], vlags: [] };
  window.__avsync = data;
  const flashes: number[] = [];
  const beeps: number[] = [];
  const sinceSecond = (t: number) => t - Math.round(t / 1000) * 1000;

  const box = document.createElement("div");
  box.style.cssText =
    "position:fixed;right:12px;bottom:12px;z-index:60;padding:8px 11px;border-radius:8px;background:rgba(10,14,20,.85);color:#e6ebf1;font:12.5px/1.5 ui-monospace,monospace;pointer-events:none;white-space:pre";
  document.body.append(box);
  const fmt = (x: number | null, sign = false) => (x === null ? "–" : `${sign && x > 0 ? "+" : ""}${Math.round(x)} ms`);
  const render = () => {
    box.textContent =
      `A/V sync, last ${Math.min(WINDOW, data.offsets.length)} beeps\n` +
      `Sync offset ${fmt(median(data.offsets.slice(-WINDOW)), true)} (sound − picture)\n` +
      `Sound lag   ${fmt(median(data.lags.slice(-WINDOW)))}\n` +
      `Picture lag ${fmt(median(data.vlags.slice(-WINDOW)))} (epoch test page, synced clocks)`;
  };
  render();

  const pair = () => {
    for (let i = 0; i < beeps.length; i++) {
      const b = beeps[i]!;
      const j = flashes.findIndex((f) => Math.abs(b - f) < PAIR_MS);
      if (j < 0) continue;
      const f = flashes[j]!;
      data.at.push(b);
      data.offsets.push(b - f);
      data.lags.push(sinceSecond(b));
      data.vlags.push(sinceSecond(f));
      beeps.splice(0, i + 1);
      flashes.splice(0, j + 1);
      render();
      return;
    }
    // Forget what can't be paired any more.
    const old = performance.timeOrigin + performance.now() - 3000;
    while (beeps.length && beeps[0]! < old) beeps.shift();
    while (flashes.length && flashes[0]! < old) flashes.shift();
  };

  // The beep.
  let quietSince = -Infinity;
  let loud = false;
  const unwatch = screen.watchSound((at, rms) => {
    if (rms < BEEP_RMS) {
      if (loud) quietSince = at;
      loud = false;
      return;
    }
    if (!loud && at - quietSince >= QUIET_MS) {
      beeps.push(at);
      pair();
    }
    loud = true;
  });

  // The flash.
  const sample = document.createElement("canvas");
  sample.width = sample.height = 16;
  const g = sample.getContext("2d", { willReadFrequently: true });
  let bright = false;
  let raf = 0;
  const tick = () => {
    raf = requestAnimationFrame(tick);
    const now = performance.timeOrigin + performance.now();
    const src = screen.video();
    // The element is the frame's: instanceof HTMLVideoElement would be false here.
    const [w, h] = src && "videoWidth" in src ? [src.videoWidth, src.videoHeight] : [src?.width ?? 0, src?.height ?? 0];
    if (!g || !src || w === 0 || h === 0) return;
    g.drawImage(src, w / 4, h / 4, w / 2, h / 2, 0, 0, 16, 16);
    const px = g.getImageData(0, 0, 16, 16).data;
    let sum = 0;
    for (let i = 0; i < px.length; i += 4) sum += 0.2126 * px[i]! + 0.7152 * px[i + 1]! + 0.0722 * px[i + 2]!;
    const luma = sum / (256 * 255);
    if (luma > FLASH_LUMA && !bright) {
      flashes.push(now);
      pair();
    }
    bright = luma > FLASH_LUMA;
  };
  raf = requestAnimationFrame(tick);

  return () => {
    cancelAnimationFrame(raf);
    unwatch();
    box.remove();
  };
}

function median(xs: number[]): number | null {
  if (xs.length === 0) return null;
  const s = [...xs].sort((a, b) => a - b);
  const m = s.length >> 1;
  return s.length % 2 ? s[m]! : (s[m - 1]! + s[m]!) / 2;
}
