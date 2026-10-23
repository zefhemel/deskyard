---
tags: component
status: built
partOf: "[[Architecture/Desktop]]"
uses:
  - "[[Architecture/Session]]"
---
Experimental mobile application/window bridge: `images/common/rootfs/usr/local/bin/deskyard-apps` runs as the desktop user through supervisor exec. Gio enumerates visible desktop entries from the user's XDG application paths (including Flatpak exports) and launches registered IDs. libwnck reports window identity, bounds, state and transient parents on the existing XFCE display, and activates selected windows. The image adds `python3-gi` and `gir1.2-wnck-3.0`; this does not open a new port or add a privileged service.

The [[Architecture/API]] exposes GET/POST `/api/desktops/{name}/applications` to desktop viewers with existing session/access/origin checks. Requests accept only launch/activate with validated IDs, execute with a 12-second deadline, and cap output at 1 MiB. Old images report that the updated helper is required.

The [[Architecture/Web UI]] polls while its application dialog or focused view is open. Browser-local crop/scale of the full Selkies iframe preserves its input mapping and includes known transient dialogs. This is a prototype: it still transmits the whole screen, shares focus/input, cannot reveal off-screen pixels, and does not independently capture covered windows or outlying popup menus. No automatic window resizing is performed. Full desktop restores the viewer's prior size preference; focused views hide agent marks.
