export interface Modifiers {
  ctrl: boolean;
  alt: boolean;
}
const plain: Modifiers = { ctrl: false, alt: false };
const special: Record<string, string> = {
  History: "\x02[",
  Escape: "\x1b",
  Tab: "\t",
  Enter: "\r",
  Backspace: "\x7f",
  ArrowUp: "\x1b[A",
  ArrowDown: "\x1b[B",
  ArrowRight: "\x1b[C",
  ArrowLeft: "\x1b[D",
  Home: "\x1b[H",
  End: "\x1b[F",
  PageUp: "\x1b[5~",
  PageDown: "\x1b[6~",
  Delete: "\x1b[3~",
};
export function encodeKey(
  key: string,
  modifiers: Modifiers = plain,
  applicationCursor = false,
): string {
  let data = special[key] ?? key;
  const parameter = 1 + (modifiers.alt ? 2 : 0) + (modifiers.ctrl ? 4 : 0);
  if (parameter > 1 && /^\x1b\[[ABCDHF]$/.test(data))
    return `\x1b[1;${parameter}${data.at(-1)}`;
  if (applicationCursor && parameter === 1 && /^\x1b\[[ABCDHF]$/.test(data))
    return "\x1bO" + data.at(-1);
  if (parameter > 1 && /^\x1b\[\d+~$/.test(data))
    return data.replace("~", `;${parameter}~`);
  if (modifiers.ctrl && key.length === 1) {
    const n = key.toUpperCase().charCodeAt(0);
    if (n >= 64 && n <= 95) data = String.fromCharCode(n & 31);
    else if (key === " ") data = "\x00";
    else if (key === "?") data = "\x7f";
  }
  return modifiers.alt ? "\x1b" + data : data;
}
/** xterm onBinary reports byte values in JS characters, not UTF-8 text. */
export function binaryBytes(data: string): Uint8Array {
  return Uint8Array.from(data, (c) => c.charCodeAt(0) & 255);
}
export class OneShotModifiers {
  value: Modifiers = { ...plain };
  toggle(key: keyof Modifiers) {
    this.value = { ...this.value, [key]: !this.value[key] };
  }
  reset() {
    this.value = { ...plain };
  }
  consume(data: string, composing = false): string {
    const value = this.value;
    this.reset();
    // A paste/composition commit is text, not a sequence of shortcut presses.
    if (composing) return data;
    const cursor = Object.entries(special).find(
      ([, bytes]) => bytes === data,
    )?.[0];
    if (cursor) return encodeKey(cursor, value);
    const application = /^\x1bO([ABCDHF])$/.exec(data);
    if (application) {
      const key = Object.entries(special).find(
        ([, bytes]) => bytes === "\x1b[" + application[1],
      )?.[0];
      if (key) return encodeKey(key, value, true);
    }
    return /^[\x20-\x7e]$/.test(data) ? encodeKey(data, value) : data;
  }
  key(key: string, applicationCursor = false): string {
    const data = encodeKey(key, this.value, applicationCursor);
    this.reset();
    return data;
  }
}
