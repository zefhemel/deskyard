import type { DeskyardScreen } from "./selkies.ts";

/** Public presentation statistics from the pinned Selkies 2.1.0 client. */
export interface StreamSample {
  t: number;
  fps: number;
  frames_not_shown: number;
  present_ms?: number;
}
export interface StatsWindow {
  document: { hidden: boolean };
  stream_stats?: { latest: StreamSample | null };
}

/** Decoder pacing and congestion control stay upstream-owned. This adds
 * presentation backpressure, without mistaking an unchanged screen for load.
 */
export class AutoFramerate {
  rate: number;
  private previous: StreamSample | null = null;
  private behind = 0;
  private healthy = 0;
  readonly ceiling: number;
  constructor(ceiling: number, rate = ceiling) { this.ceiling = ceiling; this.rate = Math.max(10, Math.min(ceiling, rate)); }
  reset() { this.previous = null; this.behind = this.healthy = 0; }
  sample(sample: StreamSample): number | null {
    if (!Number.isFinite(sample.t) || !Number.isFinite(sample.fps) || !Number.isFinite(sample.frames_not_shown)) return null;
    if (sample.fps < 0 || sample.frames_not_shown < 0) return null;
    const previous = this.previous;
    if (previous?.t === sample.t) return null;
    this.previous = sample;
    const seconds = previous ? (sample.t - previous.t) / 1000 : 0;
    if (!previous || seconds < 0.5 || seconds > 3 || sample.frames_not_shown < previous.frames_not_shown) {
      this.behind = this.healthy = 0;
      return null;
    }
    const lost = (sample.frames_not_shown - previous.frames_not_shown) / seconds;
    if (sample.fps + lost < 5) { this.behind = this.healthy = 0; return null; }
    const overloaded = lost >= 2 && lost / (sample.fps + lost) > 0.1;
    if (overloaded) {
      this.healthy = 0;
      if (++this.behind < 3) return null;
      this.behind = 0;
      const next = Math.max(10, Math.min(Math.floor(this.rate * 0.75), Math.floor(sample.fps * 0.95)));
      if (next >= this.rate) return null;
      return this.rate = next;
    }
    this.behind = 0;
    if (sample.fps < this.rate * 0.8 || (sample.present_ms ?? 0) > 100) { this.healthy = 0; return null; }
    if (++this.healthy < 20) return null;
    this.healthy = 0;
    const next = Math.min(this.ceiling, Math.ceil(this.rate * 1.1));
    if (next === this.rate) return null;
    return this.rate = next;
  }
}

/** Caller disposes this monitor on preference, connection or owner changes. */
export function startAutoQuality(screen: DeskyardScreen, target: StatsWindow, interval = globalThis.setInterval, cancel = globalThis.clearInterval): () => void {
  let policy = new AutoFramerate(60);
  let geometry = "";
  screen.post({ type: "statsOpen", open: true });
  const timer = interval(() => {
    const display = screen.display();
    if (target.document.hidden || screen.state() !== "connected" || !display.owner) { policy.reset(); return; }
    const key = `${display.width}x${display.height}:${display.settings?.encoder}`;
    if (key !== geometry) {
      geometry = key;
      const ceiling = display.settings?.encoder === "jpeg" ? 30 : 60;
      const current = Number(display.settings?.framerate);
      policy = new AutoFramerate(ceiling, Number.isFinite(current) && current > 0 ? current : ceiling);
    }
    const sample = target.stream_stats?.latest;
    if (!sample || Date.now() - sample.t > 3000) { policy.reset(); return; }
    const rate = policy.sample(sample);
    if (rate !== null) screen.post({ type: "settings", settings: { framerate: rate } });
  }, 1000);
  return () => { cancel(timer); screen.post({ type: "statsOpen", open: false }); };
}
