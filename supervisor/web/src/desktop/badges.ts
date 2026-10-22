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
  private readonly send: SendBadge;

  constructor(send: SendBadge) {
    this.send = send;
  }

  /** What the page last asked the badge to say. */
  wants(kind: Badge): boolean {
    return this.want[kind];
  }

  /**
   * Sets the badge. Resolves once the supervisor has it, or when a newer
   * report made meanwhile replaced it (then it was never sent); rejects
   * when the supervisor refused it.
   */
  set(kind: Badge, on: boolean): Promise<void> {
    this.want[kind] = on;
    const mine = ++this.seq;
    const p = this.chain.then(() => (this.want[kind] === on && this.latest[kind] === mine ? this.send(kind, on, mine) : undefined));
    this.latest[kind] = mine;
    this.chain = p.catch(() => undefined);
    return p;
  }
}
