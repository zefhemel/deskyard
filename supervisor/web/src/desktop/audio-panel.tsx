// The desktop bar's Audio button and its panel, layout B of the audio mockup
// (docs/superpowers/specs/2026-10-22-audio-and-ports-design/audio-bar.html):
// the desktop's sound here, my mic into the desktop, and my camera into it.
// Selkies does the work in the frame (selkies.ts); this is the UI, this
// browser's choices, and the mic and camera badges everyone sees
// (badges.ts).
import type { ComponentChildren } from "preact";
import { useCallback, useEffect, useMemo, useRef, useState } from "preact/hooks";
import { api, type Host } from "../api.ts";
import { CamIcon, MicIcon } from "../components/avatar.tsx";
import { notifyError } from "../components/toast.tsx";
import { cx } from "../components/ui.tsx";
import { type Badge, Badges } from "./badges.ts";
import { NeedsHttps } from "./needs-https.tsx";
import { canChooseOutput, type Capture, type DeskyardScreen, insecureContext, type Media, setCapture, setVolume } from "./selkies.ts";

// Remembered per browser. Sound is off on the first visit (sound may only
// start with a click); the mic and the webcam are never remembered: they're
// off on every load and connection.
const SOUND_KEY = "deskyard.sound"; // {on, volume, output}
const MIC_INPUT_KEY = "deskyard.mic.input";
const CAM_INPUT_KEY = "deskyard.cam.input";

interface Sound {
  on: boolean;
  volume: number;
  output: string;
}

function loadSound(): Sound {
  try {
    const s = JSON.parse(localStorage.getItem(SOUND_KEY) ?? "{}") as Partial<Sound>;
    return {
      on: s.on === true,
      volume: typeof s.volume === "number" && Number.isFinite(s.volume) ? Math.min(1, Math.max(0, s.volume)) : 1,
      output: typeof s.output === "string" ? s.output : "",
    };
  } catch {
    return { on: false, volume: 1, output: "" };
  }
}

function load(key: string): string {
  try {
    return localStorage.getItem(key) ?? "";
  } catch {
    return "";
  }
}

function save(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // private mode: not remembered
  }
}

const noMedia: Media = { sound: false, mic: false, webcam: false, playing: false, soundAt: 0 };

/**
 * How long a live mic or webcam may be off before the page takes it as
 * stopped: Selkies restarts the capture to change devices.
 */
const GAP_MS = 3000;
/** How long a camera may take to start before the page gives up on it. */
const CAM_START_MS = 30000;

const badgeOf: Record<Capture, Badge> = { microphone: "mic", webcam: "cam" };
const liveIn = (m: Media, kind: Capture) => (kind === "microphone" ? m.mic : m.webcam);

/** Where the page is: the frame's client, its connection and the badges. */
interface Here {
  screen: DeskyardScreen | null;
  connected: boolean;
  badges: Badges | null;
  /** Counts connections, so work started on one doesn't land on the next. */
  conn: number;
}

/**
 * One capture (the mic or the webcam) of this page. The badge is up whenever
 * the user wants the capture or it is live, so it is never live unseen:
 * - on: the badge goes up (and the supervisor has it) before the capture is
 *   allowed;
 * - off: the capture stops, and the badge comes down once it isn't live.
 * The badge's reports are serialised and numbered (badges.ts), so turning it
 * off and on quickly never leaves a stale "off" last.
 */
function useCapture(kind: Capture, here: { current: Here }, prepare: (s: DeskyardScreen) => void, startMs?: number) {
  const [on, setOn] = useState(false);
  const st = useRef({ intent: false, live: false, wasLive: false, gap: 0, startTimer: 0 });

  const reconcile = useCallback(() => {
    const b = here.current.badges;
    const want = st.current.intent || st.current.live;
    if (b && here.current.connected && b.wants(badgeOf[kind]) !== want) b.set(badgeOf[kind], want).catch(() => {});
  }, [kind, here]);

  const clearTimers = () => {
    window.clearTimeout(st.current.gap);
    window.clearTimeout(st.current.startTimer);
    st.current.gap = st.current.startTimer = 0;
  };

  const stop = useCallback(() => {
    const c = st.current;
    clearTimers();
    c.intent = false;
    setOn(false);
    const s = here.current.screen;
    if (s) setCapture(s, kind, false);
    reconcile();
  }, [kind, here, reconcile]);

  const start = useCallback(async () => {
    const { screen: s, connected, badges, conn } = here.current;
    const c = st.current;
    if (!s || !connected || !badges || c.intent) return;
    c.intent = true;
    c.wasLive = false;
    setOn(true);
    try {
      // Until the supervisor has the newest report: an "on" replaced by
      // later clicks settles as they do, and only an "on" it has counts.
      await badges.set(badgeOf[kind], true);
      await badges.settled(badgeOf[kind]);
    } catch (e) {
      if (here.current.conn === conn && c.intent) {
        c.intent = false;
        setOn(false);
        notifyError(e);
      }
      return;
    }
    // Turned off, or a new connection, meanwhile: the badge follows (reconcile).
    if (here.current.conn !== conn || !c.intent || !badges.wants(badgeOf[kind])) return;
    prepare(s);
    setCapture(s, kind, true);
    if (startMs) {
      c.startTimer = window.setTimeout(() => {
        if (c.intent && !c.live) {
          notifyError(`Your ${kind === "webcam" ? "camera" : "mic"} didn't start`);
          stop();
        }
      }, startMs);
    }
  }, [kind, here, prepare, startMs, stop]);

  // Selkies' state for this capture.
  const follow = useCallback(
    (m: Media) => {
      const c = st.current;
      c.live = liveIn(m, kind);
      if (c.live) {
        c.wasLive = true;
        clearTimers();
      } else if (c.intent && c.wasLive && !c.gap) {
        // Off and staying off (an error, the desktop refusing it) is off; a
        // restart for a new device comes back within GAP_MS.
        c.gap = window.setTimeout(stop, GAP_MS);
      }
      reconcile();
    },
    [kind, reconcile, stop],
  );

  // A new connection (or none): the capture is off, and so is the badge on
  // the new stream. The "off" goes after any report still on its way from
  // the last connection, so none of those lingers.
  const reset = useCallback(() => {
    clearTimers();
    st.current = { intent: false, live: false, wasLive: false, gap: 0, startTimer: 0 };
    setOn(false);
    const { badges, connected } = here.current;
    if (badges && connected) badges.set(badgeOf[kind], false).catch(() => {});
  }, [kind, here]);

  return { on, start, stop, follow, reset };
}

const barButton =
  "inline-flex cursor-pointer items-center gap-1.5 rounded-[7px] border-0 bg-transparent px-2.5 py-[7px] text-[13.5px] font-semibold whitespace-nowrap text-bar-text hover:bg-bar-hover hover:text-white";

/** Why a desktop's camera switch is off-limits, or null when it may be used. */
function noCamera(name: string, webcam: boolean, host: Host | null, admin: boolean): string | null {
  if (webcam) return null;
  if (host && host.cameras === null) return "Cameras aren't available here: the container engine isn't on this machine";
  if (!host || host.cameras?.length === 0) return "This machine has no virtual camera for environments";
  return admin ? `${name} has no camera yet; give it one in its Settings` : `${name} has no camera yet; an admin can give it one`;
}

/**
 * screen is the frame's client (null until it loads), connected whether its
 * stream is up; webcam whether the desktop has its camera.
 */
export function AudioControl({
  name,
  screen,
  connected,
  webcam,
  host,
  admin,
}: {
  name: string;
  screen: DeskyardScreen | null;
  connected: boolean;
  webcam: boolean;
  host: Host | null;
  admin: boolean;
}) {
  const [sound, setSoundPrefs] = useState(loadSound);
  const soundRef = useRef(sound);
  soundRef.current = sound;
  const [input, setInput] = useState(() => load(MIC_INPUT_KEY));
  const [camera, setCameraPref] = useState(() => load(CAM_INPUT_KEY));
  const devices = useRef({ input, camera });
  devices.current = { input, camera };
  const [open, setOpen] = useState(false);
  const [media, setMedia] = useState<Media>(noMedia);

  // One reporter per load of the frame (its tab id).
  const badges = useMemo(
    () => (screen ? new Badges((kind, on, seq) => api.capture(name, kind, on, screen.tab, seq)) : null),
    [screen, name],
  );
  const here = useRef<Here>({ screen, connected, badges, conn: 0 });
  here.current = { ...here.current, screen, connected, badges };

  const mic = useCapture(
    "microphone",
    here,
    useCallback((s: DeskyardScreen) => s.setMic(devices.current.input), []),
  );
  const cam = useCapture(
    "webcam",
    here,
    useCallback((s: DeskyardScreen) => s.setCamera(devices.current.camera), []),
    CAM_START_MS,
  );

  const updateSound = (patch: Partial<Sound>) => {
    const next = { ...soundRef.current, ...patch };
    soundRef.current = next;
    setSoundPrefs(next);
    save(SOUND_KEY, JSON.stringify(next));
  };

  // --- following the frame ----------------------------------------------------------
  useEffect(() => {
    setMedia(screen ? { ...screen.media() } : noMedia);
    if (!screen) return;
    const onMedia = (m: Media) => {
      setMedia({ ...m });
      mic.follow(m);
      cam.follow(m);
      // The volume applies once Selkies has its playback set up.
      if (m.sound) setVolume(screen, soundRef.current.volume);
    };
    const onAlert = (a: { kind: Capture; text: string }) => {
      notifyError(a.text);
      (a.kind === "microphone" ? mic : cam).stop();
    };
    const offs = [screen.on("deskyard.media", onMedia), screen.on("deskyard.alert", onAlert)];
    return () => {
      for (const f of offs) f();
    };
  }, [screen, mic.follow, mic.stop, cam.follow, cam.stop]);

  // Each connection: this browser's sound; the mic and webcam start off (the
  // last stream took their badges, and our additions refuse a capture the
  // page didn't ask for on this connection).
  useEffect(() => {
    here.current.conn++;
    mic.reset();
    cam.reset();
    if (!screen || !connected) return;
    const p = soundRef.current;
    screen.setSound(p.on);
    if (p.output) screen.setOutput(p.output);
    setVolume(screen, p.volume);
  }, [screen, connected]);

  // Sound remembered "on" after a reload: the browser holds it back until
  // the next click or key press on the page.
  const blocked = sound.on && media.sound && !media.playing;
  useEffect(() => {
    if (!blocked || !screen) return;
    const resume = () => screen.resumeSound();
    document.addEventListener("pointerdown", resume);
    document.addEventListener("keydown", resume);
    return () => {
      document.removeEventListener("pointerdown", resume);
      document.removeEventListener("keydown", resume);
    };
  }, [blocked, screen]);

  const setSound = (on: boolean) => {
    updateSound({ on });
    screen?.setSound(on);
  };
  const changeVolume = (volume: number) => {
    updateSound({ volume });
    if (screen) setVolume(screen, volume);
  };
  const changeOutput = (output: string) => {
    updateSound({ output });
    screen?.setOutput(output);
  };
  const changeInput = (id: string) => {
    setInput(id);
    save(MIC_INPUT_KEY, id);
    screen?.setMic(id);
  };
  const changeCamera = (id: string) => {
    setCameraPref(id);
    save(CAM_INPUT_KEY, id);
    const s = screen;
    if (!s) return;
    s.setCamera(id);
    if (cam.on) {
      // A restart on the new camera; the gap timer allows for it.
      setCapture(s, "webcam", false);
      setCapture(s, "webcam", true);
    }
  };

  const close = useCallback(() => setOpen(false), []);
  const camNote = noCamera(name, webcam, host, admin);
  const label = [
    blocked ? "Audio: click to play the desktop's sound" : sound.on ? "Audio: desktop sound on" : "Audio: desktop sound off",
    ...(mic.on ? ["your mic is on"] : []),
    ...(cam.on ? ["your camera is on"] : []),
  ].join("; ");

  return (
    <div class="relative">
      <button
        type="button"
        data-audio-toggle
        aria-label="Audio"
        title={label}
        aria-haspopup="dialog"
        aria-expanded={open}
        class={cx(barButton, (open || sound.on) && "bg-bar-hover text-white")}
        onClick={() => {
          if (blocked) screen?.resumeSound();
          setOpen(!open);
        }}
      >
        <SpeakerIcon on={sound.on} />
        <span class="max-sm:hidden">Audio</span>
        {mic.on && <MicIcon class="text-[#ff8b8f]" />}
        {cam.on && <CamIcon class="text-[#ff8b8f]" />}
        {blocked && <span class="h-1.5 w-1.5 rounded-full bg-busy" />}
        <span aria-hidden="true" class="text-[10px]">
          ▾
        </span>
      </button>
      {open && (
        <Panel onClose={close}>
          <Section title="Desktop sound" label="Desktop sound" checked={sound.on} onChange={setSound} disabled={!connected}>
            {blocked && <Hint>Your browser holds sound back until you click on this page</Hint>}
            {!sound.on && <Hint>Plays what's playing on {name}, here, for you only</Hint>}
            {canChooseOutput && <DevicePicker kind="audiooutput" label="Play on" value={sound.output} onChange={changeOutput} />}
            <label class="mt-2.5 flex items-center gap-2.5 text-[12.5px] text-muted">
              Volume
              <input
                type="range"
                min={0}
                max={1}
                step={0.01}
                aria-label="Volume"
                class="flex-1 accent-accent"
                value={sound.volume}
                onInput={(e) => changeVolume(Number(e.currentTarget.value))}
              />
            </label>
          </Section>
          <hr class="my-3 border-0 border-t border-hairline" />
          <Section
            title={`My mic into ${name}`}
            label="My mic"
            checked={mic.on}
            disabled={!connected || insecureContext}
            onChange={(on) => (on ? void mic.start() : mic.stop())}
          >
            {insecureContext ? (
              <NeedsHttps />
            ) : (
              <>
                <DevicePicker kind="audioinput" label="Microphone" value={input} onChange={changeInput} />
                {mic.on && <MicMeter screen={screen} live={media.mic} />}
                {mic.on && !media.mic && <Hint>Starting your mic…</Hint>}
                <Hint>Everyone on {name} sees when your mic is on</Hint>
              </>
            )}
          </Section>
          <hr class="my-3 border-0 border-t border-hairline" />
          <Section
            title={`My camera into ${name}`}
            label="My camera"
            checked={cam.on}
            disabled={!connected || camNote !== null || insecureContext}
            onChange={(on) => (on ? void cam.start() : cam.stop())}
          >
            {insecureContext ? (
              <NeedsHttps />
            ) : camNote === null ? (
              <>
                <DevicePicker kind="videoinput" label="Camera" value={camera} onChange={changeCamera} />
                {cam.on && !media.webcam && <Hint>Starting your camera…</Hint>}
                <Hint>Everyone on {name} sees when your camera is on</Hint>
              </>
            ) : (
              <Hint>{camNote}</Hint>
            )}
          </Section>
        </Panel>
      )}
    </div>
  );
}

function Panel({ onClose, children }: { onClose: () => void; children: ComponentChildren }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node;
      // The toggle button handles its own clicks.
      if (ref.current && !ref.current.contains(t) && !(t instanceof Element && t.closest("[data-audio-toggle]"))) onClose();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    // A click into the desktop's frame blurs this window.
    const onBlur = () => onClose();
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    window.addEventListener("blur", onBlur);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
      window.removeEventListener("blur", onBlur);
    };
  }, [onClose]);
  return (
    <div
      ref={ref}
      role="dialog"
      aria-label="Audio"
      class="absolute top-[42px] right-0 z-40 max-h-[calc(100dvh-58px)] w-[290px] overflow-y-auto rounded-[10px] bg-surface p-3.5 text-ink shadow-[0_18px_40px_-12px_rgba(0,0,0,.6)] max-sm:fixed max-sm:inset-x-2.5 max-sm:top-[50px] max-sm:w-auto"
    >
      {children}
    </div>
  );
}

function Section({
  title,
  label,
  checked,
  disabled,
  onChange,
  children,
}: {
  title: string;
  label: string;
  checked: boolean;
  disabled?: boolean;
  onChange: (on: boolean) => void;
  children: ComponentChildren;
}) {
  return (
    <section>
      <div class="flex items-center justify-between gap-3">
        <h3 class="truncate text-[13px] font-semibold">{title}</h3>
        <Switch label={label} checked={checked} disabled={disabled} onChange={onChange} />
      </div>
      {children}
    </section>
  );
}

function Hint({ children }: { children: ComponentChildren }) {
  return <p class="mt-1.5 text-[12.5px] text-muted">{children}</p>;
}

/**
 * The browser's devices of a kind, without its "default" and
 * "communications" aliases. Labels (and so the list) may only come once the
 * site may use the mic or camera; until then only the default is offered.
 */
function DevicePicker({ kind, label, value, onChange }: { kind: MediaDeviceKind; label: string; value: string; onChange: (id: string) => void }) {
  const [devices, setDevices] = useState<MediaDeviceInfo[]>([]);
  useEffect(() => {
    const md = navigator.mediaDevices;
    if (!md?.enumerateDevices) return;
    const load = () =>
      md
        .enumerateDevices()
        .then((ds) => setDevices(ds.filter((d) => d.kind === kind && d.deviceId && d.deviceId !== "default" && d.deviceId !== "communications")))
        .catch(() => {});
    void load();
    md.addEventListener("devicechange", load);
    return () => md.removeEventListener("devicechange", load);
  }, [kind]);
  if (devices.length === 0) return null;
  const unit = { audiooutput: "Speaker", audioinput: "Microphone", videoinput: "Camera" }[kind];
  return (
    <select aria-label={label} class="mt-2.5 h-[32px]! text-[13px]" value={value} onChange={(e) => onChange(e.currentTarget.value)}>
      <option value="">System default</option>
      {devices.map((d, i) => (
        <option key={d.deviceId} value={d.deviceId}>
          {d.label || `${unit} ${i + 1}`}
        </option>
      ))}
      {value && !devices.some((d) => d.deviceId === value) && <option value={value}>Not connected</option>}
    </select>
  );
}

/** How loud the mic is, while the panel is open. */
function MicMeter({ screen, live }: { screen: DeskyardScreen | null; live: boolean }) {
  const [level, setLevel] = useState(0);
  useEffect(() => {
    if (!screen || !live) {
      setLevel(0);
      return;
    }
    let raf = 0;
    const tick = () => {
      setLevel(screen.micLevel());
      raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [screen, live]);
  return (
    <div
      role="meter"
      aria-label="Mic level"
      aria-valuemin={0}
      aria-valuemax={1}
      aria-valuenow={Math.round(level * 100) / 100}
      class="mt-2.5 h-1.5 overflow-hidden rounded-full bg-track"
    >
      <div class="h-full bg-run" style={{ width: `${Math.round(level * 100)}%` }} />
    </div>
  );
}

function Switch({ label, checked, disabled, onChange }: { label: string; checked: boolean; disabled?: boolean; onChange: (v: boolean) => void }) {
  return (
    <button
      type="button"
      role="switch"
      aria-label={label}
      aria-checked={checked}
      disabled={disabled}
      class={cx(
        "relative h-[18px] w-[32px] flex-none cursor-pointer rounded-full border-0 p-0 transition-colors disabled:cursor-default disabled:opacity-50",
        checked ? "bg-accent" : "bg-track",
      )}
      onClick={() => onChange(!checked)}
    >
      <span class={cx("absolute top-[2px] h-[14px] w-[14px] rounded-full bg-white shadow transition-[left]", checked ? "left-[16px]" : "left-[2px]")} />
    </button>
  );
}

function SpeakerIcon({ on }: { on: boolean }) {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M2.5 6v4h2.5l3.5 3V3L5 6z" fill="currentColor" />
      {on ? (
        <>
          <path d="M10.5 5.8a3 3 0 0 1 0 4.4" />
          <path d="M12.3 4a5.5 5.5 0 0 1 0 8" />
        </>
      ) : (
        <path d="M10.5 6l4 4m0-4l-4 4" />
      )}
    </svg>
  );
}
