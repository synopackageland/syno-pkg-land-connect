#!/usr/bin/env python3
"""Render assets/icon/icon_<size>.png from assets/icon/icon.svg.

Run after editing icon.svg and commit the PNGs; needs rsvg-convert (librsvg).
Sizes cover the DSM window/desktop icons and Package Center's PACKAGE_ICON (64)
and PACKAGE_ICON_256; dsm-package.yaml `icon: assets/icon` hands them to the CLI.
"""
import subprocess
from pathlib import Path

ICON = Path(__file__).resolve().parent.parent / 'assets' / 'icon'
SIZES = [16, 24, 32, 48, 64, 72, 256]

if __name__ == '__main__':
    for size in SIZES:
        subprocess.run(['rsvg-convert', '-w', str(size), '-h', str(size), str(ICON / 'icon.svg'),
                        '-o', str(ICON / f'icon_{size}.png')], check=True)
