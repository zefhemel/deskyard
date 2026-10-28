// Auto quality: the display streams with Auto's preset (selkies.ts), and its
// frame rate follows what this browser manages to show.
import type { DeskyardScreen, Display, StreamSample } from "../selkies/screen.ts";
import type { Preset } from "./selkies.ts";

/** The policy's numbers. */
export const AUTO_FRAMERATE = {
  /** Never below this frame rate. */
  floor: 10,
  /** Samples further apart than this, in seconds, start over; closer ones are skipped. */
  minGap: 0.5,
  maxGap: 3,
  /** Shown plus lost frames per second below this: an idle screen, which says nothing. */
  minActivity: 5,
  /** Overloaded: at least this many frames per second not shown… */
  minLost: 2,
  /** …and more than this share of all frames. */
  maxLostShare: 0.1,
  /** Overloaded samples in a row before the rate goes down. */
  overloadRun: 3,
  /** Down to this share of the rate, at most… */
  lowerRate: 0.75,
  /** …and to this share of what is being shown, at least. */
  lowerShown: 0.95,
  /** Healthy: showing at least this share of the rate… */
  minShownShare: 0.8,
  /** …and presenting a frame in at most this many ms. */
  maxPresentMs: 100,
  /** Healthy samples in a row before the rate goes up… */
  healthyRun: 20,
  /** …by this factor, up to the preset's own rate. */
  raise: 1.1,
  /** How often the monitor samples, in ms. */
  intervalMs: 1000,
  /** A sample older than this, in ms, is stale. */
  staleMs: 3000,
} as const;

const P = AUTO_FRAMERATE;

/** Decoder pacing and congestion control stay upstream-owned. This adds
 * presentation backpressure, without mistaking an unchanged screen for load.
 */
export class AutoFramerate {
  rate: number;
  private previous: StreamSample | null = null;
  private behind = 0;
  private healthy = 0;
  readonly ceiling: number;
  constructor(ceiling: number, rate = ceiling) { this.ceiling = ceiling; this.rate = Math.max(P.floor, Math.min(ceiling, rate)); }
  reset() { this.previous = null; this.behind = this.healthy = 0; }
  sample(sample: StreamSample): number | null {
    if (!Number.isFinite(sample.t) || !Number.isFinite(sample.fps) || !Number.isFinite(sample.frames_not_shown)) return null;
    if (sample.fps < 0 || sample.frames_not_shown < 0) return null;
    const previous = this.previous;
    if (previous?.t === sample.t) return null;
    this.previous = sample;
    const seconds = previous ? (sample.t - previous.t) / 1000 : 0;
    if (!previous || seconds < P.minGap || seconds > P.maxGap || sample.frames_not_shown < previous.frames_not_shown) {
      this.behind = this.healthy = 0;
      return null;
    }
    const lost = (sample.frames_not_shown - previous.frames_not_shown) / seconds;
    if (sample.fps + lost < P.minActivity) { this.behind = this.healthy = 0; return null; }
    const overloaded = lost >= P.minLost && lost / (sample.fps + lost) > P.maxLostShare;
    if (overloaded) {
      this.healthy = 0;
      if (++this.behind < P.overloadRun) return null;
      this.behind = 0;
      const next = Math.max(P.floor, Math.min(Math.floor(this.rate * P.lowerRate), Math.floor(sample.fps * P.lowerShown)));
      if (next >= this.rate) return null;
      return this.rate = next;
    }
    this.behind = 0;
    if (sample.fps < this.rate * P.minShownShare || (sample.present_ms ?? 0) > P.maxPresentMs) { this.healthy = 0; return null; }
    if (++this.healthy < P.healthyRun) return null;
    this.healthy = 0;
    const next = Math.min(this.ceiling, Math.ceil(this.rate * P.raise));
    if (next === this.rate) return null;
    return this.rate = next;
  }
}

/** A screen that reports presentation statistics (the v1 adapter's). */
export type StatsScreen = DeskyardScreen & Required<Pick<DeskyardScreen, "stats" | "showStats">>;

export const hasStats = (screen: DeskyardScreen): screen is StatsScreen => !!screen.stats && !!screen.showStats;

/** The frame rate a preset streams at, in the encoding the display uses. */
export const presetFramerate = (preset: Preset, display: Display) => (display.settings?.encoder === "jpeg" ? preset.jpeg.framerate : preset.framerate);

/**
 * Adapts the display's frame rate while it streams with preset. The caller
 * starts it only while it applies (Auto chosen, this page owning a connected
 * display) and disposes it when that changes. Pauses while the page is hidden.
 */
export function startAutoQuality(
  screen: StatsScreen,
  preset: Preset,
  hidden = () => document.hidden,
  interval = globalThis.setInterval,
  cancel = globalThis.clearInterval,
): () => void {
  let policy = new AutoFramerate(preset.framerate);
  // A new size or encoder: start over from the rate it streams at now.
  let geometry = "";
  const follow = (display: Display) => {
    const key = `${display.width}x${display.height}:${display.settings?.encoder}`;
    if (key === geometry) return;
    geometry = key;
    const ceiling = presetFramerate(preset, display);
    const current = Number(display.settings?.framerate);
    policy = new AutoFramerate(ceiling, Number.isFinite(current) && current > 0 ? current : ceiling);
  };
  follow(screen.display());
  const unfollow = screen.on("deskyard.display", follow);
  screen.showStats(true);
  const timer = interval(() => {
    const sample = screen.stats();
    if (hidden() || !sample || Date.now() - sample.t > P.staleMs) { policy.reset(); return; }
    const rate = policy.sample(sample);
    if (rate !== null) screen.post({ type: "settings", settings: { framerate: rate } });
  }, P.intervalMs);
  return () => { cancel(timer); unfollow(); screen.showStats(false); };
}
