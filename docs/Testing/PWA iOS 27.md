---
displayName: PWA testing — iOS 27
---
The mobile prototype was installed through Safari's actual Add to Home Screen flow on an iPhone 18 Pro simulator running iOS 27.0 (24A434), using Xcode 27 Device Hub. The test used the plain HTTP LAN endpoint and the restricted demo account. This is native simulator evidence, not a physical-device claim.

# Verified
- Safari → Share → View More → Add to Home Screen showed the Deskyard name/icon with Open as Web App enabled. The installed icon launched the desktop wall without Safari's address bar, retained the demo session, and opened the live desktop inside the app. Web Inspector reported both `navigator.standalone` and the standalone display-mode query as true.
- The native software keyboard opened using Keyboard after disconnecting the simulator's hardware keyboard. Tapping the on-screen P, W and A keys entered `pwa` in the remote terminal. The test text was removed without execution; Keyboard dismissed the software keyboard and the full view returned.
- Portrait → landscape → upright portrait preserved the connected stream and reachable controls. Apps opened in landscape and selected an existing terminal; Full desktop returned to the complete screen.
- Backgrounded to Home Screen, launched the installed app again through Spotlight, and confirmed the stream was connected, Trackpad mode was still enabled, the view height was 874 and page scroll was zero.
- The native top-edge blur was visible over the first toolbar row before the solid buffer. A 16-pixel strip below the top safe inset kept the toolbar text crisp with Liquid Glass left at its normal setting.

# Viewport findings
Initially the installed app reported a full screen/large viewport of 874 CSS pixels, but `innerHeight`, `clientHeight`, `100dvh` and `visualViewport.height` were 812; the top safe area was 62 pixels. With the keyboard open, `innerHeight` stayed 812 and `visualViewport.height` became 505. Treating those values as the entire screen left unused space. The adapter now restores only the measured gap between the large viewport and layout height before applying the visual viewport, and standalone layout uses the large viewport. After the complete layout correction and reload, both JavaScript heights were 874 at rest. No hardcoded phone dimensions are used.

Apple documents [standalone Home Screen metadata](https://developer.apple.com/library/archive/documentation/AppleApplications/Reference/SafariWebContent/ConfiguringWebApplications/ConfiguringWebApplications.html). The implementation's iOS 27 spacing and height correction are based on the measurements above, rather than an assumption about all iOS versions.

# Verification
`make typecheck` passed 23 UI tests; `make build` and `make test` (Go/race) passed. The PWA, application/touch and desktop-page browser suites passed 22 tests, with 7 Firefox skips and 7 deselections. The explicitly marked WebKit PWA test also passed separately. Review corrected its missing WebKit marker so the default suite retains its Chromium-only installation requirement. One GTK fixture captured button positions before XFCE finished configuring/reparenting its window; it now refreshes coordinates on configure events and reads the current values before each tap. The full rerun passed.

# Screenshots
[Play the native software-keyboard walkthrough](PWA%20iOS%2027/keyboard-demo.mp4). The 25-second clip is scaled/sped up for chat and does not include installation or rotation.

![Standalone portrait app](PWA%20iOS%2027/portrait-standalone.png)

![Safe-area-aware Apps dialog](PWA%20iOS%2027/apps-dialog.png)

![Native software keyboard input in the PWA](PWA%20iOS%2027/software-keyboard.png)

![Focused application in landscape](PWA%20iOS%2027/landscape-focused.png)

![Top-edge blur before the buffer](PWA%20iOS%2027/top-edge-before.png)

# Limits
Physical iPhone behavior, background suspension over long periods, and native two-finger pinch/trackpad swipes remain unverified in this simulator session. Chromium multi-touch regression tests cover pinch, panning, relative pointer behavior and edge following. Plain HTTP retains the browser's mic/camera/clipboard restrictions. The app requires a network connection and the supervisor; no authenticated content is cached for offline use.
