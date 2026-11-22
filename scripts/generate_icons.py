#!/usr/bin/env python3
"""Generate Weloury's full icon set from the root weloury.png.

Outputs (mirroring `cargo tauri icon`'s conventional set):
  apps/desktop/icons/icon.png          1024x1024 (bundle source + Linux window icon)
  apps/desktop/icons/32x32.png ...     window/UI sizes
  apps/desktop/icons/icon.ico          Windows app/installer icon (multi-size)
  apps/desktop/icons/icon.icns         macOS app icon
  apps/desktop/icons/Square*.png       Windows installer tiles
  apps/desktop/icons/StoreLogo.png     Windows store logo

Run from the repo root:  python3 scripts/generate_icons.py
Requires Pillow (pip install pillow).
"""

from pathlib import Path

from PIL import Image

ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / "weloury.png"
OUT = ROOT / "apps" / "desktop" / "icons"

# Sizes Tauri's icon set conventionally ships, per filename.
PNG_SIZES = {
    "32x32.png": 32,
    "128x128.png": 128,
    "128x128@2x.png": 256,
    "icon.png": 1024,
    "Square30x30Logo.png": 30,
    "Square44x44Logo.png": 44,
    "Square71x71Logo.png": 71,
    "Square89x89Logo.png": 89,
    "Square107x107Logo.png": 107,
    "Square142x142Logo.png": 142,
    "Square150x150Logo.png": 150,
    "Square284x284Logo.png": 284,
    "Square310x310Logo.png": 310,
    "StoreLogo.png": 50,
}

# ICO wants explicit, classic sizes.
ICO_SIZES = [(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)]


def main() -> None:
    src = Image.open(SOURCE).convert("RGBA")

    # Center-crop to a square before downscaling, so the mark is never
    # distorted regardless of the source file's aspect ratio.
    side = min(src.size)
    left = (src.width - side) // 2
    top = (src.height - side) // 2
    src = src.crop((left, top, left + side, top + side))

    OUT.mkdir(parents=True, exist_ok=True)

    rendered: dict[int, Image.Image] = {}
    for name, size in PNG_SIZES.items():
        img = src.resize((size, size), Image.LANCZOS)
        img.save(OUT / name, "PNG", optimize=True)
        rendered[size] = img
        print(f"wrote {OUT / name} ({size}x{size})")

    img = src.resize((256, 256), Image.LANCZOS)
    img.save(OUT / "icon.ico", sizes=ICO_SIZES)
    print(f"wrote {OUT / 'icon.ico'} ({', '.join(f'{w}x{h}' for w, h in ICO_SIZES)})")

    # The .icns embeds the full 1024x1024 master as its largest member
    # (macOS scales down from it for Retina surfaces); Pillow writes the
    # classic multi-size ic09..ic10 set from the single source image.
    master = src.resize((1024, 1024), Image.LANCZOS)
    master.save(OUT / "icon.icns", "ICNS")
    print(f"wrote {OUT / 'icon.icns'} (1024x1024 master, multi-size)")


if __name__ == "__main__":
    main()
