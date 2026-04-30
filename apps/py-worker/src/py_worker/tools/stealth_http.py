"""
Stealth HTTP fetch with JA4+ impersonation via rnet.

We target chrome/safari/firefox presets depending on the target.
Unlike Playwright, rnet does not execute JS — so this is the first tier
of the scraping ladder and should handle 30-40% of Cloudflare/DataDome-
protected sites at browser-free cost.

Note on rnet API surface (rnet 3.x):
- The browser/client preset enum is `rnet.Emulation` (was `Impersonate` in 2.x).
- `rnet.Client` takes an `emulation=` kwarg and a `timeout=datetime.timedelta` kwarg.
- `Client.get/post/...` return coroutines.
"""
from __future__ import annotations

import datetime
import time
from typing import Any

import rnet
from pydantic import BaseModel, Field


class StealthHttpInput(BaseModel):
    url: str
    method: str = "GET"
    impersonate: str = Field(default="chrome", pattern="^(chrome|firefox|safari|safari_ios|okhttp|edge)$")
    headers: dict[str, str] = Field(default_factory=dict)
    body: str | None = None
    timeout_ms: int = 15000
    follow_redirects: bool = True


class StealthHttpOutput(BaseModel):
    status: int
    url: str
    headers: dict[str, str]
    body: str
    took_ms: int
    impersonate: str


def _resolve_emulation(name: str) -> Any:
    """
    Map friendly names to rnet.Emulation enum values. rnet's enum names are
    version-specific (e.g. Chrome145, Firefox147, Safari26); we probe at call
    time and pick the most recent variant matching each family.
    """
    available = [x for x in dir(rnet.Emulation) if not x.startswith("_")]

    if name == "safari":
        # Must exclude SafariIos, SafariIPad, SafariIpad variants
        candidates = [
            a for a in available
            if a.startswith("Safari")
            and not a.startswith("SafariIos")
            and not a.startswith("SafariIPad")
            and not a.startswith("SafariIpad")
        ]
    elif name == "safari_ios":
        candidates = [a for a in available if a.startswith("SafariIos")]
    elif name == "chrome":
        candidates = [a for a in available if a.startswith("Chrome")]
    elif name == "firefox":
        # Plain Firefox (not Private/Android) preferred
        plain = [
            a for a in available
            if a.startswith("Firefox")
            and not a.startswith("FirefoxPrivate")
            and not a.startswith("FirefoxAndroid")
        ]
        candidates = plain or [a for a in available if a.startswith("Firefox")]
    elif name == "edge":
        candidates = [a for a in available if a.startswith("Edge")]
    elif name == "okhttp":
        candidates = [a for a in available if a.startswith("OkHttp")]
    else:
        raise RuntimeError(f"unknown emulation family: {name}")

    if not candidates:
        raise RuntimeError(f"no rnet.Emulation variant found for {name}")

    # Sort lexicographically and pick the last — good proxy for "latest version" for
    # these zero-padded-ish names (Chrome145, Firefox147, Safari26, OkHttp5, Edge145).
    chosen = sorted(candidates)[-1]
    return getattr(rnet.Emulation, chosen)


async def stealth_http(input: dict[str, Any]) -> dict[str, Any]:
    parsed = StealthHttpInput.model_validate(input)
    emulation = _resolve_emulation(parsed.impersonate)

    client = rnet.Client(
        emulation=emulation,
        timeout=datetime.timedelta(milliseconds=parsed.timeout_ms),
    )
    start = time.perf_counter()

    if parsed.method.upper() == "GET":
        resp = await client.get(parsed.url, headers=parsed.headers)
    elif parsed.method.upper() == "POST":
        resp = await client.post(parsed.url, headers=parsed.headers, body=parsed.body or "")
    else:
        raise ValueError(f"unsupported method: {parsed.method}")

    body_text = await resp.text()

    # rnet 3.x returns a `StatusCode` object (not an int); use `.as_int()`.
    status = resp.status.as_int() if hasattr(resp.status, "as_int") else int(resp.status)
    # rnet 3.x `HeaderMap` is iterable as (bytes, bytes) tuples — no `.items()`.
    headers: dict[str, str] = {}
    for k, v in resp.headers:
        key = k.decode("latin-1") if isinstance(k, (bytes, bytearray)) else str(k)
        val = v.decode("latin-1") if isinstance(v, (bytes, bytearray)) else str(v)
        headers[key] = val
    out = StealthHttpOutput(
        status=status,
        url=str(resp.url),
        headers=headers,
        body=body_text,
        took_ms=int((time.perf_counter() - start) * 1000),
        impersonate=parsed.impersonate,
    )
    return out.model_dump()
