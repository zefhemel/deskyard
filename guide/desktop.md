# Using the desktop

The environment's page in the browser (`http://host/<name>/`): the desktop,
its controls on phones and tablets, and its shared terminals.

The XFCE desktop is streamed with
[Selkies](https://github.com/selkies-project/selkies): picture, sound, your
mic and gamepads. It resizes to fit your browser window (or keeps a locked
size). The bar above it shows the connection as a dot and the VPN as a shield,
and has icon buttons (with tooltips) for sound and mic, Display (lock size,
resolution and quality), the clipboard, keys the browser would otherwise take
(Ctrl+Alt+Del, Alt+Tab, ...), and **Share** for people with full access who
didn't get in through a share. Drop files on the screen to copy them into the
environment. Badges show who has their mic on.
The bar's tabs show the desktop next to the environment's shared terminal
sessions. On phones, Display and Keys are in the scrollable More
menu, and the tabs get a row of their own.

**Enter fullscreen** (the expand icon in the top bar) fills the browser screen with the Deskyard tab bar and current desktop or terminal view. The same button becomes **Exit fullscreen**; the browser’s Escape action also exits. Switching tabs keeps fullscreen active, and menus and dialogs remain usable. Leaving the environment page exits fullscreen started with this button. The button appears only when the browser supports fullscreen; it needs no desktop image update.

Mouse-wheel and two-finger trackpad gestures scroll the application under the
pointer in the desktop. Small trackpad movements accumulate rather than being
discarded; horizontal gestures scroll horizontally where the application supports
it. The desktop fix is served by the updated supervisor for images with the v1
client contract; older images need an image upgrade.

The bar also shows terminal clients and programs working in the environment
alongside browser viewers, with what they are running, recent actions, and
marks on the screen where they click and type. Typed text and secrets on
command lines are hidden. Live UI updates and these marks use WebSockets, so
several viewer tabs also work over plain HTTP without occupying the browser's
limited HTTP connections.

## Paste an image

Paste a screenshot or copied image into the desktop view or a terminal tab
(**⌘V** on Mac, or the browser’s image-capable Paste action). **Save pasted image** suggests an editable full path under `~/Desktop/`;
edit it if you like, then choose **Upload image** to save it at that path.
Clipboard images with generic names get a timestamped `screenshot-….png`
name (or the image's format). Existing files are kept: the upload adds a
number if the name is taken. Cancel leaves the image unuploaded; a failed
upload shows an error and lets you retry. Use `~/` or an absolute path to choose another location writable by the desktop user. Missing parent folders are created. The final absolute path appears after upload and is copied to your clipboard; a copy failure is reported so you can copy it manually.

This uses the image supplied by the browser's paste event. Ordinary text
paste continues as before, and the terminal toolbar's Paste button still
pastes text. Some plain-text paste shortcuts omit clipboard images. As with file drops, uploads require full access to a running
environment.

## Retina and display scaling

The **Display** menu offers **Scaling: Auto**, **1×** and **2×**. Auto chooses
2× when your browser reports a pixel density of at least 1.5, including Retina
Macs, and follows changes as you move between displays. Browser zoom also
influences this detection. Choose 1× or 2× to override it; the choice is saved
in your browser. On narrow screens these choices are in **⋯ More**.

At 2× the desktop renders twice as many pixels in each direction and XFCE
scales its fonts, GTK controls, panel and cursor. Resolution presets describe
logical dimensions: 1280×800 at 2× streams 2560×1600. Auto fit follows the
available viewport. The stream fits proportionally within 3840×2160, so larger
windows may not achieve a full two physical pixels per logical pixel.

The first viewer controls resolution and scaling for the shared desktop.
Other viewers fit that same picture to their windows. Some applications may
need reopening to adopt a scale change. Full scaling support requires both an
updated supervisor and an updated desktop image; upgrade existing environments
from their image settings after the new image is available.

## Mobile controls

The **Display** menu has **Lock size** and **Resolution:** choices that pick
a remote screen size and lock it, plus the quality presets.
On phones, open **⋯ More** for the same choices. Presets include
800×600, 1024×768, 1280×720, 1280×800, 1920×1080, and portrait sizes
600×900 and 900×1200. **Resolution: Auto fit** unlocks the size. The choice
is remembered in your browser; the first viewer controls the shared screen.
More explains when another viewer controls the shared resolution.

On a phone, a desktop at the phone's own size is too small to use, so until
you choose otherwise the screen starts locked at 1280×720 and touch starts in
**Trackpad** mode. A phone here is a screen whose shorter side is under 640
pixels, in either orientation; tablets and computers start with Auto fit and
**Tap**. Choosing Auto fit, another size, or Tap replaces the default and is
remembered.

Touch controls are available on touch devices, including landscape phones
and tablets:

- **Tap** clicks where your finger touches; **Trackpad** moves the pointer
  relative to your finger. In a magnified view, moving the
  pointer to an edge pans your view to follow it. The choice is remembered
  in your browser.
- **Keyboard** shows or hides the phone's software keyboard. Esc, Tab,
  Enter and Paste are alongside it (swipe the extra keys if space is short).
- **Pinch** magnifies your own view, up to 4×. With the view magnified,
  move two fingers together to pan; **Fit screen** resets magnification.
  At fit, a two-finger swipe scrolls the remote application as usual.

Magnification is local: it does not zoom the application or resize the shared
remote screen. The controls remain outside the magnified picture. The page
follows the visible viewport when the software keyboard opens. These touch
controls are served by the supervisor and work with any Selkies desktop image.

## Install on an iPhone Home Screen

In Safari, open Deskyard, sign in, then choose **Share → View More → Add to
Home Screen** (the exact share-sheet layout depends on iOS). Leave **Open
as Web App** enabled and tap **Add**. Launch the Deskyard icon to open the
app without Safari's address bar; choose an environment from the list.

The installed app supports portrait and landscape, keeps controls clear of
the Dynamic Island and home indicator, and resizes around the software
keyboard. On iOS, a solid strip below the status bar keeps the system's
top-edge blur away from toolbar text. It needs the running supervisor and
a network connection; remote desktop sessions are not cached for offline use.
Installation and native keyboard input were verified in the iOS 27 iPhone
simulator, including on the plain HTTP LAN demo. HTTPS is still required
for the mic and automatic clipboard synchronization; see [On a LAN](running.md#on-a-lan).

## Shared terminal

The environment's page (`http://host/alice/`) shows its desktop and its shared terminal sessions as tabs: **Desktop** first, then one tab per running session (including ones opened from the desktop's own terminal, the CLI or another browser; a session opened or ended anywhere shows up within about a second), then **+**. A terminal tab's address is `http://host/alice/?terminal=build-2`, and a small count on the tab shows how many others are attached. **+** opens a new session named `terminal-1`, `terminal-2`, and so on, skipping names retained by attachment groups after a rename. Switching tabs keeps each terminal connected and its scrollback; leaving the Desktop tab stops its stream, and returning reconnects it. Drag a tab, or press Alt+Shift+← or → on it, to reorder; the order is remembered in this browser. A terminal tab's bar has Paste, Keys and Text size.

The small **⋯** button immediately left of **×** offers **Detach** and **Rename session…**. **Detach** closes only this page’s tab and connection; the shell, its jobs and other clients keep running. It stays hidden through session-list updates on this page. Reload the page or open the session’s URL to show it again. **Rename session…** changes its name. Enter a valid, unused session name (names retained by tmux attachment groups are reserved); its shell and programs keep running. This browser reconnects using the new name. Other browser tabs discover the renamed session in their session list; old bookmarked terminal URLs no longer join it. Renaming requires an updated desktop image with the rename helper.

**×** on a tab ends that session for everyone: its shell, every program in it, and every other client's view. It asks first when a program other than the shell is running in it, or when anyone else is attached; otherwise it ends at once. A link or a reconnect only joins: a session that no longer exists opens the Desktop tab with a "session ended" note and is never recreated. Each running environment's card lists **Desktop** and its sessions (the first three and "+N more" when there are more than four), and **+** opens `main`, creating it if needed, or a new session when `main` already exists.

The create/join walkthrough, screenshots, native keyboard evidence and known limits are in the project space's *Testing/Shared terminal 2026-10-23* page, and the tabs' checks in *Testing/Environment tabs 2026-10-26* (see [Project records](development.md#project-records)). The page reconnects after brief network interruptions without replaying disconnected input. Restarting an environment ends its shells and jobs.

The terminal keyboard bar is hidden by default on devices whose primary pointer supports fine movement and hover (typically a laptop or desktop), and shown on touch-first devices. Browsers do not report physical keyboard attachment reliably. Use **Keys → Show keyboard bar** (or **⋯ → Show keyboard bar** on phones) to override the choice for the current terminal tab. Hiding it gives the terminal the space back and clears any one-shot Ctrl/Alt modifiers.

On phones, tap **Keyboard** to open the software keyboard. The compact, single-row key bar scrolls sideways and provides Ctrl+C/D/L, Esc, Tab, arrows, Home/End, PgUp/PgDn and Delete, plus one-shot Ctrl and Alt. Helper taps keep the keyboard open; paste and composition clear modifiers. On short keyboard viewports the environment toolbar shares the bottom key row as a horizontally scrollable strip. If the browser leaves less than the minimum space for a readable terminal row and 44px controls, the page asks you to rotate to portrait or dismiss the keyboard; the shell stays connected. **History** opens tmux history: use PgUp/PgDn, then Esc to return. Mouse-wheel and
two-finger trackpad gestures over a terminal tab also scroll its shared tmux
history; the first upward movement scrolls immediately. Mouse-aware applications
(such as `less --mouse` or Vim with mouse support enabled) receive the wheel
events themselves. For local text selection in a browser terminal, hold Shift
while dragging on Linux/Windows, or Option on Mac. These terminal defaults need
an updated desktop image; Mac Option selection also needs the updated supervisor. The font stays readable when clients have different sizes; a passive smaller client may show a cropped tmux view until it interacts.

`deskyard -e alice terminal` joins the environment's shared Bash login shell in `/workspace`; `deskyard terminal build-2` selects another named shell. Inside the environment, ordinary terminal windows and tabs (including **Deskyard Terminal**, the default terminal shortcut and file-manager terminal actions) each open a new shared shell named `terminal-1`, `terminal-2`, and so on, preserving their starting directory. The window/tab title shows the session name, and the browser shows it as a tab. When sessions are already running, the desktop's **Deskyard Terminal** launcher first lists them with their attached clients and running program: type a number to join one, or press Enter for a new session. Within a native shared terminal, press **Ctrl-B then s** and choose its name; outside tmux, `deskyard terminal NAME` joins it. Browser clients switch tabs; remote CLI clients detach and reconnect with the chosen name. Saved emulator defaults migrate at desktop startup; explicit emulator command launches such as `xfce4-terminal -x PROGRAM` still run the requested program directly. Run `deskyard terminal` locally to join `main`. Session names contain 1–64 letters, digits, underscores or dashes and start with a letter or digit. Detach with Ctrl-B then D; reconnecting joins the same shell and running jobs. Sessions end when the environment stops or restarts. Existing environments need an updated image with tmux and the shared terminal helper.

### Switch tabs with the keyboard

Ctrl+Shift+1 selects Desktop; Ctrl+Shift+2–9 selects the first eight terminal tabs in the viewer’s current order. Use Control on Mac too. Shortcuts work with focus inside the desktop iframe or browser terminal, ignore composition and extra Alt/Meta modifiers, leave missing positions untouched, and pause while a dialog is open. Physical number-row keys are used so Shift-produced punctuation does not change the mapping. Tab tooltips and `aria-keyshortcuts` expose the bindings.
