#!/usr/bin/env python3
"""Pack app/Assets/AppIcon-1024.png into AppIcon.icns (all Retina sizes).

The master artwork is the 1024×1024 PNG (macOS Big Sur+ microVM cube with a
cyan terminal caret). Regenerate the .icns after editing the master:

  python3 app/scripts/generate_icon.py
  python3 app/scripts/generate_icon.py path/to/out.icns

On macOS you can also redraw a vector approximation with:
  swift app/scripts/generate-icon.swift app/Assets/AppIcon.icns
"""
from __future__ import annotations

import io
import struct
import sys
from pathlib import Path

from PIL import Image

ROOT = Path(__file__).resolve().parents[1]
MASTER = ROOT / "Assets" / "AppIcon-1024.png"
DEFAULT_ICNS = ROOT / "Assets" / "AppIcon.icns"

# icns type → pixel size (PNG-compressed entries used by modern macOS)
ICNS_PNG = [
    ("ic07", 128),
    ("ic08", 256),
    ("ic09", 512),
    ("ic10", 1024),
    ("ic11", 32),   # 16@2x
    ("ic12", 64),   # 32@2x
    ("ic13", 256),  # 128@2x
    ("ic14", 512),  # 256@2x
]


def png_bytes(img: Image.Image) -> bytes:
    buf = io.BytesIO()
    img.save(buf, format="PNG", optimize=True, compress_level=9)
    return buf.getvalue()


def write_icns(path: Path, master: Image.Image) -> None:
    chunks: list[bytes] = []
    for tag, px in ICNS_PNG:
        scaled = master.resize((px, px), Image.Resampling.LANCZOS)
        data = png_bytes(scaled)
        chunks.append(tag.encode("ascii") + struct.pack(">I", 8 + len(data)) + data)
    body = b"".join(chunks)
    path.write_bytes(b"icns" + struct.pack(">I", 8 + len(body)) + body)


def main() -> int:
    out = Path(sys.argv[1]) if len(sys.argv) > 1 else DEFAULT_ICNS
    if not MASTER.is_file():
        print(f"error: missing master artwork {MASTER}", file=sys.stderr)
        return 1
    master = Image.open(MASTER).convert("RGBA")
    if master.size != (1024, 1024):
        master = master.resize((1024, 1024), Image.Resampling.LANCZOS)
    write_icns(out, master)
    print(f"wrote {out} ({out.stat().st_size} bytes) from {MASTER.name}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
