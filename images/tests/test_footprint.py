import json
import os
import subprocess
import time

import pytest
from playwright.sync_api import sync_playwright

from conftest import Volumes
from sandbox import TOKEN_HEADER, start_sandbox

GIB = 1024 ** 3
PAGE = "data:text/html,<title>footprint</title>" + "<p>" + "lorem ipsum " * 2000 + "</p>"


def parse_mem(s: str) -> float:
    value = s.split("/")[0].strip()
    units = {"KiB": 1024, "MiB": 1024 ** 2, "GiB": GIB, "B": 1}
    for unit, mult in units.items():
        if value.endswith(unit):
            return float(value[: -len(unit)]) * mult
    raise ValueError(s)


@pytest.mark.slow
def test_three_instances_fit(image, artifacts_dir):
    vols = [Volumes() for _ in range(3)]
    boxes = []
    try:
        for v in vols:
            boxes.append(start_sandbox(image, home=v.home))
        with sync_playwright() as p:
            for sb in boxes:
                browser = p.chromium.connect_over_cdp(sb.cdp_ws(), headers=TOKEN_HEADER)
                browser.contexts[0].new_page().goto(PAGE)
        time.sleep(20)
        out = subprocess.run(["docker", "stats", "--no-stream", "--format", "{{json .}}",
                              *[sb.name for sb in boxes]], capture_output=True, text=True, check=True).stdout
        stats = [json.loads(line) for line in out.splitlines()]
        (artifacts_dir / "stats.json").write_text(json.dumps(stats, indent=2))
        (artifacts_dir / "desktop.png").write_bytes(boxes[0].exec_bytes("deskyard screenshot"))
        mem = {s["Name"]: parse_mem(s["MemUsage"]) for s in stats}
        print({k: f"{v / GIB:.2f} GiB" for k, v in mem.items()})
        if os.environ.get("AC_DESKTOP", "xfce") != "xfce":
            return  # New variants record stats; only XFCE has an established budget.
        assert all(m <= 1.2 * GIB for m in mem.values()), mem
        assert sum(mem.values()) <= 3.6 * GIB, mem
    finally:
        for sb in boxes:
            sb.remove()
        for v in vols:
            v.remove()
