Commercial licensing assessment, reviewed 2026-10-23. Deskyard appears commercially sellable, including with proprietary supervisor and UI code, subject to third-party license compliance. No reviewed base-product license prohibits selling the product. This is an engineering assessment, not legal clearance; H.264 patents and the boundary of combined GPL works need legal review before release.

The optional `examples/retroarch/` image added on 2026-10-24 is outside that commercially permissive conclusion: it bundles Snes9x and Genesis Plus GX cores with explicit non-commercial restrictions. Their pinned source archives, checksums, original license files and build instructions are retained with the example image. Its `NOTICES.md` distinguishes these restrictions from the GPL-covered cores, RetroArch, Flatpak runtimes and the base image; the example is not a commercially unrestricted distribution. The five core source pins do not pin the moving Flathub application/runtime or Debian compiler packages. Resolved Flatpak commits are recorded at `/usr/share/doc/deskyard-retroarch/flatpak-commits.txt`; distributors still need the exact component inventory, notices and corresponding source required by their final artifact.

The implemented streaming stack is Selkies, per [[superpowers/specs/2026-10-22-selkies-design|Selkies screen design]]. The production `images/Dockerfile` pins Selkies 2.1.0, pixelflux 2.2.1 and pcmflux 2.2.0; pixelflux and its codec libraries are built from pinned source with Deskyard's idle-grab patch, while Selkies and pcmflux come from PyPI. The original assessment inspected these versions' PyPI metadata, Selkies' universal wheel and CPython 3.13 manylinux wheels for x86_64 and aarch64, with downloaded hashes matching PyPI. The source-built pixelflux runtime must be inventoried from the final artifact rather than assuming the inspected wheel's contents are identical.

The [[superpowers/specs/2026-10-23-desktop-variants-design|Desktop image variants design]] adds Debian-packaged KDE Plasma and MATE plus Discover or GNOME Software and their Flatpak backends. MATE also includes Debian-packaged Arc, Papirus icons and Noto fonts for its selected appearance. These expand the package/license inventory for each final image; a review of XFCE alone does not cover their actual dependency closures. LibreOffice is offered through the per-user Flathub catalog and is not bundled in these base images. Admin recipes that preinstall it or other apps need a separate artifact inventory.

# Commercial models

| Delivery | Assessment | Main obligations |
| --- | --- | --- |
| Hosted desktops on infrastructure you operate | Commercial use is allowed by the reviewed open-source licenses. | Browser-delivered software still counts as distribution. Preserve notices and provide the source required for the Selkies web client. Ordinary GPL and MPL do not require disclosure merely for running server code over a network. |
| Supervisor and CLI binaries | The reviewed Go and UI dependencies support proprietary commercial distribution. | Include applicable copyright notices, license texts and source-availability notices for any covered components actually included. |
| Supervisor or desktop container images | Commercial distribution is allowed, but the images contain independently licensed software, including GPL components. | Fulfil corresponding-source obligations for the exact distributed components, preserve notices, and keep LGPL components replaceable where required. |

Charging money and keeping source private are different questions. Customers retain the redistribution rights granted by the licenses of the open-source parts. A commercial EULA must preserve those rights. The [GNU license FAQ](https://www.gnu.org/licenses/gpl-faq.en.html#MereAggregation) explains that separate programs can form an aggregate without all becoming GPL, while the legal boundary depends on both communication and integration.

Deskyard's separate Go supervisor, HTTP/WebSocket proxy and browser adapter support treating its own code as independent from the streamer. That is an architectural assessment, not a guarantee that every future integration is independent. Copying GPL implementation code or linking it into a Deskyard executable would require a fresh assessment.

# Selkies and media

Selkies itself is MPL-2.0, including its web client. Modified covered files remain MPL when distributed; independent files in a larger work can use other licenses. The [MPL FAQ](https://www.mozilla.org/en-US/MPL/2.0/FAQ/) specifically treats browser-delivered JavaScript as distribution and minified JavaScript as executable form: provide a clear way to obtain its preferred source, including your modifications.

The [Selkies licensing inventory](https://docs.selkies.io/latest/licensing) identifies pixelflux and pcmflux as MPL-2.0, with LGPL native libraries and other separately licensed dependencies. The inspected [pixelflux 2.2.1 wheels](https://pypi.org/pypi/pixelflux/2.2.1/json) bundle libx264 and x265 under GPL-2.0-or-later; their included `LICENSES.md` explicitly describes the resulting combined binary as GPL-covered. They also bundle libde265, libvpx, SVT-AV1 and dav1d. No FFmpeg shared libraries were found in these pinned manylinux wheels; do not infer their contents from changing upstream documentation. The [pcmflux 2.2.0 wheels](https://pypi.org/pypi/pcmflux/2.2.0/json) bundle libpulse and its separately licensed dependencies, with no GPL-only component identified in their included inventory.

The planned CPU x264 path therefore needs GPL compliance when shipped. Corresponding source must cover the applicable combined streaming component and its build/install material, not just a link to x264. This does not automatically make the separate supervisor GPL. Whether additional imported or linked streaming code forms a combined work needs assessment against the actual build. The bundled libde265 uses LGPL-3.0-or-later, so the final combination's effective GPL version also needs review; do not treat the upstream GPL-2.0-or-later description as permission to choose GPLv2-only without checking compatibility.

The pinned pixelflux inventory documents a source-build option, `PIXELFLUX_ENABLE_GPL=0`, which substitutes OpenH264 and kvazaar. This is a build option, not a runtime switch that removes GPL libraries from a prebuilt wheel. It can reduce streaming GPL obligations but retains MPL/LGPL obligations and does not remove the desktop's other GPL software. A reproducible non-GPL build has not been performed in this audit.

Both pinned native wheels explicitly say their inventories do not include all third-party license texts/notices. Shipping upstream wheels unchanged is therefore not enough to demonstrate compliant notice delivery; Deskyard needs to collect the omitted material as well as fulfil source obligations.

H.264 patent permissions are separate from copyright licenses. A GPL or BSD license alone does not settle them. [Cisco's OpenH264 FAQ](https://www.openh264.org/faq.html) distinguishes Cisco's qualifying distributed binary module from software you build or distribute yourself; its royalty arrangement does not automatically cover a source-built codec bundled in pixelflux. The planned paid H.264 desktop service or product warrants a specific patent assessment. An OpenH264 rebuild should not be presented as resolving that issue.

# Deskyard application dependencies

All 34 direct and indirect modules in `supervisor/go.mod` have cached exact-version license files. The code licenses found were Apache-2.0 (19), MIT (8), BSD-3-Clause (5), ISC (1) and CC0-1.0 (1). None requires publishing Deskyard source merely because the module is linked into its binaries.

| License | Modules, with versions pinned in `supervisor/go.mod` |
| --- | --- |
| Apache-2.0 | caddyserver/certmagic; containerd/errdefs and errdefs/pkg; moby/moby/api and moby/moby/client; distribution/reference; docker/go-connections and go-units; go-logr/logr and stdr; mholt/acmez/v3; moby/docker-image-spec; opencontainers/go-digest and image-spec; opentelemetry auto/sdk, contrib/instrumentation/net/http/otelhttp, otel, otel/metric and otel/trace |
| MIT | Microsoft/go-winio; caddyserver/zerossl; felixge/httpsnoop; klauspost/cpuid/v2; libdns/libdns; go.uber.org/multierr, zap and zap/exp |
| BSD-3-Clause | miekg/dns; golang.org/x/crypto, x/sys, x/net and x/text |
| ISC | coder/websocket |
| CC0-1.0 | zeebo/blake3 |

Two notice details need preservation: `opencontainers/go-digest` separately licenses its documentation under CC-BY-SA-4.0, while its code is Apache-2.0; `klauspost/cpuid/v2` includes Go-derived BSD code alongside its MIT root license. Redistributing copied dependency documentation can carry obligations beyond those for compiled code.

The generated `supervisor/internal/engine/docker/flatpak-seccomp.json` is also third-party material. `scripts/gen-seccomp.py` derives it from `moby/profiles` at `seccomp/v0.2.4`, whose [license is Apache-2.0](https://raw.githubusercontent.com/moby/profiles/seccomp/v0.2.4/LICENSE). Include that license and preserve the modification notice.

The npm lockfile contains 172 dependency entries, counting optional platform variants: MIT 106, Apache-2.0 23, MPL-2.0 24, ISC 7, BSD-2-Clause 7, BSD-3-Clause 2, OFL-1.1 2 and CC-BY-4.0 1. No entry lacks a declared license. These are lockfile declarations, not a source-header audit of all npm packages.

- Runtime UI: Preact 11.0.1, preact-iso 2.12.2 and associated rendering code use MIT licenses.
- Public Sans and IBM Plex Mono 5.3.0 use OFL-1.1, confirmed in the installed font packages. Include their notices and license; respect reserved names for modified fonts. The [OFL permits commercial software bundles](https://openfontlicense.org/), while restricting sale of fonts by themselves.
- Vite, Tailwind and the Preact build plugins are permissively licensed; TypeScript uses Apache-2.0.
- Lightning CSS and its platform packages use MPL-2.0, but run at build time in this configuration. Their use to generate CSS does not by itself place Deskyard CSS under MPL.
- caniuse-lite data uses CC-BY-4.0. It is build data here; include attribution if distributing the dataset itself.

# Images, tools and tests

The shared desktop Dockerfile installs Debian trixie, XFCE, Chromium, Flatpak, shell and computer-use utilities; copies Caddy and uv binaries; downloads mise; and installs Node and Python through mise. The supervisor runtime image uses Alpine 3.22 with CA certificates and tzdata. These are additional dependencies beyond the application manifests.

| Component | Licensing assessment |
| --- | --- |
| Debian, XFCE and desktop utilities | A mixture of GPL, LGPL, BSD, MIT and other licenses. Debian main is intended to permit commercial redistribution under its [Free Software Guidelines](https://www.debian.org/social_contract#guidelines); fulfil each package's terms. XFCE applications and libraries have [different licenses](https://xfce.readthedocs.io/latest/faq.html). |
| Chromium | BSD-3-Clause for the core, plus many third-party licenses. Preserve its complete credits and source obligations for covered bundled libraries, rather than treating the entire binary as BSD. Chromium maintains a [third-party license inventory mechanism](https://github.com/chromium/chromium/blob/main/tools/licenses/licenses.py). |
| Alpine supervisor image | Includes BusyBox, [GPL-2.0-only](https://pkgs.alpinelinux.org/package/v3.22/main/x86_64/busybox). Shipping the image requires its corresponding-source compliance even though Deskyard's Go dependencies are permissive. |
| Caddy 2.11.7 | [Apache-2.0](https://github.com/caddyserver/caddy/blob/v2.11.7/LICENSE), with its compiled dependencies requiring their own notices. |
| uv 0.13.4 | MIT OR Apache-2.0; the pinned [MIT license](https://raw.githubusercontent.com/astral-sh/uv/0.13.4/LICENSE-MIT) permits commercial redistribution. Inspect bundled dependencies too. |
| mise | [MIT](https://github.com/jdx/mise/blob/main/LICENSE); installed tools have their own licenses. |
| Node and Python runtimes | MIT and PSF-family licensing respectively, plus bundled third-party code. Keep their complete license material. Python's managed runtime build can add native libraries. |
| Go, Node build stages, TypeScript, Vite, CI actions | Build tools are not automatically runtime dependencies. Preserve obligations for any runtime code or assets they actually place in the output. |

The locally rebuilt Flatpak CLI and shared library include an upstream libglnx retry backport. Their complete corresponding Debian source package (original source, Debian patch archive including the added patch, and `.dsc`) is shipped at `/usr/share/doc/flatpak/deskyard-source/`. Existing Debian copyright and license files remain installed with the binary packages. The build and patch are recorded in [[Architecture/Desktop runtime]].

The two Python test lockfiles use pytest, Playwright, Pillow, requests, PyYAML and their dependencies, with websockets additionally in supervisor tests. Installed metadata identifies permissive MIT, Apache, BSD, PSF and MIT-CMU licenses, except certifi's MPL-2.0 certificate bundle. These test environments and Playwright browser downloads are not copied into production by the checked Dockerfiles. Test dependencies do not force the application to adopt their license.

User-installed Flatpak applications, mise tools and agent CLIs require their own review if bundled into a commercial custom image. Flathub availability is not blanket permission to redistribute an application. The base image's instructions for installing Claude Code and Codex do not themselves bundle those products or confer resale rights for their services.

Docker Engine and Docker Desktop have different terms. [Docker Desktop](https://docs.docker.com/subscription-billing/desktop-license) requires paid subscriptions outside its qualifying free uses; this is not a per-copy royalty on Deskyard. [OrbStack](https://orbstack.dev/pricing) limits its free tier to personal, non-commercial use. Development and customer deployments must use suitable licenses for those host tools.

# Release work still required

No tracked root project license or third-party notice bundle was found. That does not stop the copyright owner from selling their own code, but the intended proprietary or open-source terms need an explicit choice, and rights to any outside contributions need to be established. This assessment does not select a project license.

The checked release rules build images and upload bare binaries with checksums. They contain no explicit process for collecting third-party notices or preserving corresponding source. Some upstream packages already include license files, but the release process does not demonstrate complete compliance.

Before commercial distribution:

1. Generate an SBOM and license inventory for each final image digest and architecture, including Debian/Alpine packages, wheel-native libraries, copied binaries, fonts and generated/copied assets.
2. Distribute required notices with binaries and images, and expose the web client's applicable notices and source location to browser users.
3. Archive and provide the exact corresponding source, patches, packaging and build/install scripts required by GPL/LGPL components. An upstream homepage or a Dockerfile that downloads binaries is not a substitute. Choose a compliant distribution method for each applicable license version.
4. Preserve LGPL replacement/relinking rights and carve open-source rights out of commercial restrictions.
5. Confirm the final Selkies pins, client modifications, combined-work boundaries and H.264 patent position.

The remaining uncertainty is artifact-level completeness, not an identified prohibition on commercial sales. The manifests, build rules, cached Go licenses, npm declarations, font licenses, Python test metadata, pinned streaming wheels and upstream licensing references were reviewed. Exact final container contents, every nested tool dependency, the complete Selkies Python dependency closure and all client/vendor assets have not been verified. Mutable base tags and apt repositories also mean a source-tree review cannot establish every package version in a published image. This page is not an SBOM or a complete third-party notice file.
