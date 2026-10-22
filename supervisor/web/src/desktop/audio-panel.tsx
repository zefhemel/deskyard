// The desktop bar's Audio button and its panel, layout B of the audio mockup
// (docs/superpowers/specs/2026-10-22-audio-and-ports-design/audio-bar.html):
// the desktop's sound here, my mic into the desktop, and my camera into it.
// Selkies does the work in the frame (selkies.ts); this is the UI, this
// browser's choices and the mic badge everyone sees (POST …/mic).
import type { ComponentChildren } from "preact";
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { api } from "../api.ts";
import { MicIcon } from "../components/avatar.tsx";
import { notifyError } from "../components/toast.tsx";
import { cx } from "../components/ui.tsx";
import { canChooseOutput, type DeskyardScreen, type Media, setCapture, setVolume } from "./selkies.ts";

const SOUND_KEY = "deskyard.sound";

/**
 * Remembered per browser: sound on or off (off on the first visit, since
 * sound may only start with a click), the volume and the devices. The mic
 * and the webcam never are: they're off on every load and connection.
 */
interface Prefs {
  on: boolean;
  volume: number;
  output: string;
  input: string;
  camera: string;
}

const defaults: Prefs = { on: false, volume: 1, output: "", input: "", camera: "" };

function loadPrefs(): Prefs {
  try {
    const s = JSON.parse(localStorage.getItem(SOUND_KEY) ?? "{}") as Partial<Prefs>;
    const str = (v: unknown) => (typeof v === "string" ? v : "");
    return {
      on: s.on === true,
      volume: typeof s.volume === "number" && Number.isFinite(s.volume) ? Math.min(1, Math.max(0, s.volume)) : defaults.volume,
      output: str(s.output),
      input: str(s.input),
      camera: str(s.camera),
    };
  } catch {
    return defaults;
  }
}

function savePrefs(p: Prefs) {
  try {
    localStorage.setItem(SOUND_KEY, JSON.stringify(p));
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

/** A capture the user turned on, on one connection (the frame's screen). */
interface Capture {
  screen: DeskyardScreen | null;
  on: boolean;
  live: boolean;
  gap: number | undefined;
}

const idle = (): Capture => ({ screen: null, on: false, live: false, gap: undefined });

function until(f: () => boolean, ms: number): Promise<void> {
  return new Promise((resolve) => {
    const end = Date.now() + ms;
    const tick = () => (f() || Date.now() > end ? resolve() : window.setTimeout(tick, 100));
    tick();
  });
}

const barButton =
  "inline-flex cursor-pointer items-center gap-1.5 rounded-[7px] border-0 bg-transparent px-2.5 py-[7px] text-[13.5px] font-semibold whitespace-nowrap text-bar-text hover:bg-bar-hover hover:text-white";

/**
 * screen is the frame's client (null until it loads), connected whether its
 * stream is up; webcam whether the desktop has a virtual camera.
 */
export function AudioControl({
  name,
  screen,
  connected,
  webcam,
}: {
  name: string;
  screen: DeskyardScreen | null;
  connected: boolean;
  webcam: boolean;
}) {
  const [prefs, setPrefs] = useState(loadPrefs);
  const latest = useRef(prefs);
  latest.current = prefs;
  const [open, setOpen] = useState(false);
  const [media, setMedia] = useState<Media>(noMedia);
  const [micOn, setMicOn] = useState(false);
  const [camOn, setCamOn] = useState(false);
  const mic = useRef<Capture>(idle());
  const cam = useRef<Capture>(idle());

  const update = (patch: Partial<Prefs>) => {
    const next = { ...latest.current, ...patch };
    latest.current = next;
    setPrefs(next);
    savePrefs(next);
  };

  // --- the mic: the badge goes up before it's live, and down after ------------
  const stopMic = useCallback(
    async (s: DeskyardScreen) => {
      const m = mic.current;
      if (m.screen !== s || !m.on) return;
      mic.current = idle();
      window.clearTimeout(m.gap);
      setMicOn(false);
      setCapture(s, "microphone", false);
      await until(() => !s.media().mic, 2000);
      // (A closed stream has taken its badge with it already.)
      api.mic(name, false, s.tab).catch(() => {});
    },
    [name],
  );

  const startMic = async () => {
    const s = screen;
    if (!s || !connected || mic.current.on) return;
    const m: Capture = { screen: s, on: true, live: false, gap: undefined };
    mic.current = m;
    setMicOn(true);
    try {
      await api.mic(name, true, s.tab);
    } catch (e) {
      if (mic.current === m) {
        mic.current = idle();
        setMicOn(false);
        notifyError(e);
      }
      return;
    }
    if (mic.current !== m) {
      api.mic(name, false, s.tab).catch(() => {}); // turned off meanwhile
      return;
    }
    s.setMic(latest.current.input);
    setCapture(s, "microphone", true);
  };

  // --- the webcam ---------------------------------------------------------------
  const stopCam = useCallback((s: DeskyardScreen) => {
    const c = cam.current;
    if (c.screen !== s || !c.on) return;
    cam.current = idle();
    window.clearTimeout(c.gap);
    setCamOn(false);
    setCapture(s, "webcam", false);
  }, []);

  const startCam = () => {
    const s = screen;
    if (!s || !connected || !webcam || cam.current.on) return;
    cam.current = { screen: s, on: true, live: false, gap: undefined };
    setCamOn(true);
    s.setCamera(latest.current.camera);
    setCapture(s, "webcam", true);
  };

  // --- following the frame ----------------------------------------------------------
  useEffect(() => {
    setMedia(screen ? { ...screen.media() } : noMedia);
    if (!screen) return;
    // A capture that went off and stayed off (an error, the desktop refusing
    // it) is off; a restart for a new device comes back within GAP_MS.
    const follow = (ref: { current: Capture }, live: boolean, stop: (s: DeskyardScreen) => void) => {
      const c = ref.current;
      if (c.screen !== screen || !c.on) return;
      if (live) {
        c.live = true;
        window.clearTimeout(c.gap);
        c.gap = undefined;
      } else if (c.live && c.gap === undefined) {
        c.gap = window.setTimeout(() => stop(screen), GAP_MS);
      }
    };
    const onMedia = (m: Media) => {
      setMedia({ ...m });
      follow(mic, m.mic, (s) => void stopMic(s));
      follow(cam, m.webcam, stopCam);
      // The volume applies once Selkies has its playback set up.
      if (m.sound) setVolume(screen, latest.current.volume);
    };
    const onAlert = (text: string) => {
      notifyError(text);
      if (/^Microphone/.test(text)) void stopMic(screen);
      if (/^Webcam/.test(text)) stopCam(screen);
    };
    const offs = [screen.on("deskyard.media", onMedia), screen.on("deskyard.alert", onAlert)];
    return () => {
      for (const f of offs) f();
    };
  }, [screen, stopMic, stopCam]);

  // Each connection: this browser's sound; the mic and webcam start off (the
  // stream's end took the badge, and our additions refuse a capture the page
  // didn't ask for on this connection).
  useEffect(() => {
    for (const ref of [mic, cam]) {
      window.clearTimeout(ref.current.gap);
      ref.current = idle();
    }
    setMicOn(false);
    setCamOn(false);
    if (!screen || !connected) return;
    const p = latest.current;
    screen.setSound(p.on);
    if (p.output) screen.setOutput(p.output);
    setVolume(screen, p.volume);
  }, [screen, connected]);

  // Sound remembered "on" after a reload: the browser holds it back until
  // the next click or key press on the page.
  const blocked = prefs.on && media.sound && !media.playing;
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
    update({ on });
    screen?.setSound(on);
  };
  const changeVolume = (volume: number) => {
    update({ volume });
    if (screen) setVolume(screen, volume);
  };
  const changeOutput = (output: string) => {
    update({ output });
    screen?.setOutput(output);
  };
  const changeInput = (input: string) => {
    update({ input });
    screen?.setMic(input);
  };
  const changeCamera = (camera: string) => {
    update({ camera });
    const s = screen;
    if (!s) return;
    s.setCamera(camera);
    if (cam.current.on && cam.current.screen === s) {
      // A restart on the new camera; the gap timer allows for it.
      setCapture(s, "webcam", false);
      setCapture(s, "webcam", true);
    }
  };

  const close = useCallback(() => setOpen(false), []);
  const label = [
    blocked ? "Audio: click to play the desktop's sound" : prefs.on ? "Audio: desktop sound on" : "Audio: desktop sound off",
    ...(micOn ? ["your mic is on"] : []),
    ...(camOn ? ["your camera is on"] : []),
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
        class={cx(barButton, (open || prefs.on) && "bg-bar-hover text-white")}
        onClick={() => {
          if (blocked) screen?.resumeSound();
          setOpen(!open);
        }}
      >
        <SpeakerIcon on={prefs.on} />
        <span class="max-sm:hidden">Audio</span>
        {micOn && <MicIcon class="text-[#ff8b8f]" />}
        {blocked && <span class="h-1.5 w-1.5 rounded-full bg-busy" />}
        <span aria-hidden="true" class="text-[10px]">
          ▾
        </span>
      </button>
      {open && (
        <Panel onClose={close}>
          <Section title="Desktop sound" label="Desktop sound" checked={prefs.on} onChange={setSound} disabled={!connected}>
            {blocked && <Hint>Your browser holds sound back until you click on this page</Hint>}
            {!prefs.on && <Hint>Plays what's playing on {name}, here, for you only</Hint>}
            {canChooseOutput && <DevicePicker kind="audiooutput" label="Play on" value={prefs.output} onChange={changeOutput} />}
            <label class="mt-2.5 flex items-center gap-2.5 text-[12.5px] text-muted">
              Volume
              <input
                type="range"
                min={0}
                max={1}
                step={0.01}
                aria-label="Volume"
                class="flex-1 accent-accent"
                value={prefs.volume}
                onInput={(e) => changeVolume(Number(e.currentTarget.value))}
              />
            </label>
          </Section>
          <hr class="my-3 border-0 border-t border-hairline" />
          <Section
            title={`My mic into ${name}`}
            label="My mic"
            checked={micOn}
            disabled={!connected}
            onChange={(on) => (on ? void startMic() : screen && void stopMic(screen))}
          >
            <DevicePicker kind="audioinput" label="Microphone" value={prefs.input} onChange={changeInput} />
            {micOn && <MicMeter screen={screen} live={media.mic} />}
            {micOn && !media.mic && <Hint>Starting your mic…</Hint>}
            <Hint>Everyone on {name} sees when your mic is on</Hint>
          </Section>
          <hr class="my-3 border-0 border-t border-hairline" />
          <Section
            title={`My camera into ${name}`}
            label="My camera"
            checked={camOn}
            disabled={!connected || !webcam}
            onChange={(on) => (on ? startCam() : screen && stopCam(screen))}
          >
            {webcam ? (
              <>
                <DevicePicker kind="videoinput" label="Camera" value={prefs.camera} onChange={changeCamera} />
                {camOn && !media.webcam && <Hint>Starting your camera…</Hint>}
              </>
            ) : (
              <Hint>This machine has no virtual camera for desktops</Hint>
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
      class="absolute top-[42px] right-0 z-40 w-[290px] rounded-[10px] bg-surface p-3.5 text-ink shadow-[0_18px_40px_-12px_rgba(0,0,0,.6)] max-sm:fixed max-sm:inset-x-2.5 max-sm:top-[50px] max-sm:w-auto"
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
