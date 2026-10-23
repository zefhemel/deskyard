"""Prepare the pinned client's presentation overrides; never rewrite terminal traffic."""
import base64
import hashlib
import json
from pathlib import Path
import sys
import urllib.request

ASSETS = {
    "index.html": "74fae7389f277a6791a8aaee8f1dfff642f58c7e25fd5ce65ae1df283f1c1a89",
    "app.js": "62bc20ca3e12f36314d9eef9d355cde2346407d16e7b170fcc9c1be30e924b26",
}


def main():
    output = Path(sys.argv[1])
    (output / "assets").mkdir(parents=True, exist_ok=True)
    source = {}
    for name, checksum in ASSETS.items():
        url = "https://raw.githubusercontent.com/zellij-org/zellij/v0.45.1/zellij-client/assets/" + name
        with urllib.request.urlopen(url, timeout=30) as response:
            body = response.read()
        if hashlib.sha256(body).hexdigest() != checksum:
            raise RuntimeError("Pinned web asset checksum mismatch: " + name)
        source[name] = body.decode()
    app = source["app.js"]
    for before, after in [
        ('"Hi from Zellij!"', '"Deskyard Terminal"'),
        ('document.title = "Zellij"', 'document.title = "Deskyard Terminal"'),
    ]:
        if app.count(before) != 1:
            raise RuntimeError("Pinned branding literal changed: " + before)
        app = app.replace(before, after)
    body = app.encode()
    integrity = "sha384-" + base64.b64encode(hashlib.sha384(body).digest()).decode()
    original_integrity = "sha384-" + base64.b64encode(hashlib.sha384(source["app.js"].encode()).digest()).decode()
    page = source["index.html"].replace("Zellij Web Client", "Deskyard Terminal")
    page = page.replace("assets/favicon.ico", "assets/deskyard.svg")
    page = page.replace(original_integrity, integrity)
    (output / "index.html").write_text(page)
    (output / "assets/app.js").write_bytes(body)
    manifest = {
        "name": "Deskyard Terminal", "short_name": "Deskyard",
        "start_url": "../main", "scope": "../", "display": "standalone",
        "theme_color": "#000000", "background_color": "#000000",
        "icons": [{"src": "icon-192.png", "type": "image/png", "sizes": "192x192"},
                  {"src": "icon-512.png", "type": "image/png", "sizes": "512x512"}],
    }
    (output / "assets/manifest.webmanifest").write_text(json.dumps(manifest))


if __name__ == "__main__":
    main()
