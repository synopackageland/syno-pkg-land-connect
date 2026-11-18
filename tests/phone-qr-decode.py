"""Decode every QR code in the "On your phone" screenshots and check that each
one encodes the phone browser page on a catalog address.
Run: python3 -I tests/phone-qr-decode.py <directory-of-png> (needs OpenCV)."""
import pathlib
import re
import sys

import cv2

PAGE = re.compile(r"^https?://(?:192\.0\.2\.\d+|(?:[a-z0-9-]+\.)*example\.(?:com|org|workers\.dev)|[a-z0-9.-]+)(?::\d+)?/m$")


def decode(path):
    image = cv2.imread(str(path))
    gray = cv2.cvtColor(image, cv2.COLOR_BGR2GRAY)
    _, binary = cv2.threshold(gray, 128, 255, cv2.THRESH_BINARY)
    detectors = [cv2.QRCodeDetector()]
    if hasattr(cv2, "QRCodeDetectorAruco"):
        detectors.append(cv2.QRCodeDetectorAruco())
    for source in (image, binary):
        for scale in (1, 2, 3):
            big = cv2.resize(source, None, fx=scale, fy=scale, interpolation=cv2.INTER_NEAREST)
            for detector in detectors:
                ok, texts, _, _ = detector.detectAndDecodeMulti(big)
                found = sorted({t for t in texts if t}) if ok else []
                if found:
                    return found
    return []


def main():
    folder = pathlib.Path(sys.argv[1])
    bad = 0
    for png in sorted(folder.glob("*.png")):
        texts = decode(png)
        status = "ok" if texts and all(PAGE.match(t) for t in texts) else "FAIL"
        bad += status != "ok"
        print(f"{status}\t{png.name}\t{' | '.join(texts) or '(no code found)'}")
    sys.exit(1 if bad else 0)


main()
