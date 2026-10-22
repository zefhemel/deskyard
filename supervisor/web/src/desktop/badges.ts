// A page's mic and camera badges: what it tells the supervisor, for everyone
// on the desktop to see whose mic or camera is on.
//
// One tab's reports go one at a time, numbered, so the supervisor takes them
// in the order they were made (it ignores one numbered lower than the last it
// applied). A report still waiting when a newer one for the same capture is
// made is dropped: the latest intent wins.

export type Badge = "mic" | "cam";

export type SendBadge = (kind: Badge, on: boolean, seq: number) => Promise<void>;

export class Badges {
  private seq = 0;
  private chain: Promise<unknown> = Promise.resolve();
  private want: Record<Badge, boolean> = { mic: false, cam: false };
  private latest: Record<Badge, number> = { mic: 0, cam: 0 };
  private results: Record<Badge, Promise<void>> = { mic: Promise.resolve(), cam: Promise.resolve() };
  private readonly send: SendBadge;

  constructor(send: SendBadge) {
    this.send = send;
  }

  /** What the page last asked the badge to say. */
  wants(kind: Badge): boolean {
    return this.want[kind];
  }

  /**
   * Sets the badge. Resolves once the supervisor has it; rejects when the
   * supervisor refused it. A report replaced by a newer one for the same
   * badge before it went is never sent, and settles as that newer one does:
   * resolving means the supervisor has the badge's latest state, which may
   * not be this one (check what is wanted now).
   */
  set(kind: Badge, on: boolean): Promise<void> {
    this.want[kind] = on;
    const mine = ++this.seq;
    this.latest[kind] = mine;
    let replaced = false;
    const turn = this.chain.then(() => {
      if (this.latest[kind] !== mine) {
        replaced = true;
        return undefined;
      }
      return this.send(kind, on, mine);
    });
    this.chain = turn.catch(() => undefined);
    // At the time it is dropped, the newest report for the badge is what
    // replaced this one (its own result, not waiting on this one's).
    const result: Promise<void> = turn.then(() => (replaced ? this.results[kind] : undefined));
    this.results[kind] = result;
    return result;
  }

  /**
   * Waits until the newest report for the badge has settled (rejecting if
   * the supervisor refused it): then the supervisor has wants(kind).
   */
  async settled(kind: Badge): Promise<void> {
    for (;;) {
      const r = this.results[kind];
      await r;
      if (r === this.results[kind]) return;
    }
  }
}
