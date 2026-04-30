"""
exif_extract_geolocate — fetch an image, extract EXIF tags, decode GPS to
WGS-84 lat/lon when present.

Photos uploaded to social media are usually stripped of EXIF, but the
HUGE class of OSINT cases where this matters is *reuploaded* / leaked
imagery: stock-photo dumps, breach pastes, anonymous tip uploads,
personal cloud-storage misconfigurations. Worth running cheaply on every
suspect image.
"""
from __future__ import annotations

import io
import time
from typing import Any

import exifread
import httpx


def _gps_to_decimal(coords: list[Any], ref: str) -> float | None:
    """Convert exifread's ratio-tuple format (degrees, minutes, seconds) → decimal."""
    try:
        deg, minute, second = (
            float(coords[0].num) / float(coords[0].den),
            float(coords[1].num) / float(coords[1].den),
            float(coords[2].num) / float(coords[2].den),
        )
    except Exception:
        return None
    decimal = deg + minute / 60.0 + second / 3600.0
    if ref in ("S", "W"):
        decimal = -decimal
    return decimal


async def exif_extract_geolocate(input: dict[str, Any]) -> dict[str, Any]:
    url = (input.get("url") or "").strip()
    if not url:
        raise ValueError("input.url required (image URL)")
    timeout_s = int(input.get("timeout_seconds", 20))

    start = time.perf_counter()
    async with httpx.AsyncClient(timeout=timeout_s, follow_redirects=True) as client:
        resp = await client.get(url, headers={
            "User-Agent": "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)",
        })
        resp.raise_for_status()
        data = resp.content

    tags = exifread.process_file(io.BytesIO(data), details=False)
    flat: dict[str, str] = {}
    for k, v in tags.items():
        try:
            flat[k] = str(v)
        except Exception:
            continue

    lat: float | None = None
    lon: float | None = None
    alt: float | None = None
    map_url: str | None = None
    if "GPS GPSLatitude" in tags and "GPS GPSLongitude" in tags:
        lat = _gps_to_decimal(
            tags["GPS GPSLatitude"].values, str(tags.get("GPS GPSLatitudeRef", "N"))
        )
        lon = _gps_to_decimal(
            tags["GPS GPSLongitude"].values, str(tags.get("GPS GPSLongitudeRef", "E"))
        )
        if lat is not None and lon is not None:
            map_url = f"https://www.google.com/maps?q={lat:.6f},{lon:.6f}"
    if "GPS GPSAltitude" in tags:
        try:
            v = tags["GPS GPSAltitude"].values[0]
            alt = float(v.num) / float(v.den)
        except Exception:
            alt = None

    interesting = {
        "Image Make": flat.get("Image Make"),
        "Image Model": flat.get("Image Model"),
        "Image Software": flat.get("Image Software"),
        "EXIF DateTimeOriginal": flat.get("EXIF DateTimeOriginal"),
        "EXIF LensModel": flat.get("EXIF LensModel"),
        "EXIF FocalLength": flat.get("EXIF FocalLength"),
        "Image Orientation": flat.get("Image Orientation"),
    }
    interesting = {k: v for k, v in interesting.items() if v}

    return {
        "url": url,
        "size_bytes": len(data),
        "tag_count": len(flat),
        "interesting": interesting,
        "geo": {"lat": lat, "lon": lon, "altitude_m": alt, "google_maps": map_url} if lat else None,
        "all_tags": flat,
        "took_ms": int((time.perf_counter() - start) * 1000),
        "source": "exifread",
    }
