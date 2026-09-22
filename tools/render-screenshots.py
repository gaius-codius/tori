#!/usr/bin/env python3
"""Render tools/genshots ANSI dumps into docs/screenshots/*.png."""
from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "docs" / "screenshots"
ANSI_DIR = Path("/tmp/tori-shots")
SHOTS = ("search", "library", "downloads", "help")

ANSI_RE = re.compile(r"\x1b\[([0-9;]*)m")
BG = (12, 8, 12)
FG = (203, 191, 171)


def find_font(size: int) -> ImageFont.FreeTypeFont:
    for p in Path("/usr/share/fonts").rglob("*JetBrainsMono*Regular*.ttf"):
        return ImageFont.truetype(str(p), size)
    for p in Path("/usr/share/fonts").rglob("*Mono*Regular*.ttf"):
        return ImageFont.truetype(str(p), size)
    raise SystemExit("no monospace font found under /usr/share/fonts")


def parse(text: str):
    rows, row = [], []
    fg, bold = FG, False
    i = 0
    while i < len(text):
        if text[i] == "\x1b" and i + 1 < len(text) and text[i + 1] == "[":
            m = ANSI_RE.match(text, i)
            if not m:
                i += 1
                continue
            params = [p for p in m.group(1).split(";") if p != ""]
            if not params:
                fg, bold = FG, False
            else:
                nums = [int(p) for p in params]
                j = 0
                while j < len(nums):
                    n = nums[j]
                    if n == 0:
                        fg, bold = FG, False
                    elif n == 1:
                        bold = True
                    elif n in (2, 22):
                        bold = False
                    elif n == 39:
                        fg = FG
                    elif n == 38 and j + 4 < len(nums) and nums[j + 1] == 2:
                        fg = (nums[j + 2], nums[j + 3], nums[j + 4])
                        j += 4
                    elif n == 38 and j + 1 < len(nums) and nums[j + 1] == 5:
                        j += 1
                    j += 1
            i = m.end()
            continue
        if text[i] == "\n":
            rows.append(row)
            row = []
            i += 1
            continue
        if text[i] == "\r":
            i += 1
            continue
        row.append((text[i], fg))
        i += 1
    if row:
        rows.append(row)
    while rows and not rows[-1]:
        rows.pop()
    return rows


def render(ansi_path: Path, out_path: Path, font: ImageFont.FreeTypeFont, cw: int, line_h: int):
    rows = parse(ansi_path.read_text(encoding="utf-8", errors="replace"))
    cols = max((len(r) for r in rows), default=1)
    pad, radius, chrome, margin = 28, 18, 36, 40
    width = cols * cw + pad * 2
    height = len(rows) * line_h + pad * 2
    img = Image.new("RGB", (width + margin * 2, height + chrome + margin * 2), (24, 20, 28))
    draw = ImageDraw.Draw(img)
    body = [margin, margin, margin + width, margin + chrome + height]
    draw.rounded_rectangle(body, radius=radius, fill=BG)
    for i, col in enumerate([(255, 95, 86), (255, 189, 46), (39, 201, 63)]):
        x = margin + 16 + i * 18
        y = margin + 14
        draw.ellipse([x, y, x + 10, y + 10], fill=col)
    ox, oy = margin + pad, margin + chrome + pad // 2
    for ri, row in enumerate(rows):
        x = ox
        y = oy + ri * line_h
        for ch, fg in row:
            draw.text((x, y), ch, font=font, fill=fg)
            x += cw
    out_path.parent.mkdir(parents=True, exist_ok=True)
    img.save(out_path, "PNG", optimize=True)
    print(f"wrote {out_path} {img.size}")


def main() -> int:
    genshots = Path("/tmp/genshots")
    if not genshots.exists():
        subprocess.check_call(["go", "build", "-o", str(genshots), "./tools/genshots"], cwd=ROOT)
    ANSI_DIR.mkdir(parents=True, exist_ok=True)
    for shot in SHOTS:
        env = {"COLORTERM": "truecolor", "TERM": "xterm-256color", **dict(**{k: v for k, v in __import__("os").environ.items()})}
        with open(ANSI_DIR / f"{shot}.ansi", "w", encoding="utf-8") as f:
            subprocess.check_call([str(genshots), shot], cwd=ROOT, env=env, stdout=f)
    font = find_font(16)
    tmp = Image.new("RGB", (50, 50))
    d = ImageDraw.Draw(tmp)
    bbox = d.textbbox((0, 0), "M", font=font)
    cw, ch = bbox[2] - bbox[0], bbox[3] - bbox[1]
    line_h = ch + 6
    for shot in SHOTS:
        render(ANSI_DIR / f"{shot}.ansi", OUT / f"{shot}.png", font, cw, line_h)
    return 0


if __name__ == "__main__":
    sys.exit(main())
