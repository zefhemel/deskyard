# A minimal type 2 AppImage for test_appimage.py: the upstream runtime plus a
# squashfs whose AppRun records how it was launched.
FROM debian:trixie-slim AS build
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl squashfs-tools
RUN case "$TARGETARCH" in \
        amd64) arch=x86_64 sum=2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d ;; \
        arm64) arch=aarch64 sum=00cbdfcf917cc6c0ff6d3347d59e0ca1f7f45a6df1a428a0d6d8a78664d87444 ;; \
    esac \
    && curl -fsSL -o /runtime "https://github.com/AppImage/type2-runtime/releases/download/20251108/runtime-$arch" \
    && echo "$sum  /runtime" | sha256sum -c
RUN mkdir -p /AppDir/usr/share/icons \
    && printf '#!/bin/sh\necho "$*" > "$HOME/appimage-probe.out"\n' > /AppDir/AppRun \
    && chmod +x /AppDir/AppRun \
    && printf '[Desktop Entry]\nType=Application\nName=Probe App\nExec=AppRun --probe %%U\nIcon=probe\nCategories=Utility;\n' > /AppDir/probe.desktop \
    && printf '<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8"/>\n' > /AppDir/usr/share/icons/probe.svg \
    && ln -s usr/share/icons/probe.svg /AppDir/.DirIcon \
    && mksquashfs /AppDir /app.squashfs -root-owned -noappend -quiet \
    && cat /runtime /app.squashfs > /probe.AppImage && chmod +x /probe.AppImage

FROM scratch
COPY --from=build /probe.AppImage /
