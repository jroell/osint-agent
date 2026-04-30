"""
holehe_check — checks an email address against ~120 sites by abusing each
site's password-reset / signup error response. Uses the holehe library
in-process. Most sites have *some* form of response that says "yeah, an
account exists with that email" without notifying the account holder.

CAVEAT: holehe modules go stale fast. Many sites have patched the detection
patterns since the library's last update; expect false negatives. Sites that
still work today (Twitter, Instagram, Spotify, Pinterest) are usually the most
useful signal — but treat low-signal hits as suggestive, not conclusive.
"""
from __future__ import annotations

import asyncio
import importlib
import inspect
import pkgutil
import time
from typing import Any

import httpx
import holehe.modules


def _discover_modules() -> list[tuple[str, Any]]:
    """Walk holehe.modules.* and collect every (provider_name, callable) pair."""
    found: list[tuple[str, Any]] = []
    for _, modpath, ispkg in pkgutil.walk_packages(
        holehe.modules.__path__, prefix="holehe.modules."
    ):
        if ispkg:
            continue
        try:
            mod = importlib.import_module(modpath)
        except Exception:
            continue
        provider = modpath.rsplit(".", 1)[-1]
        # Each holehe module exposes an async fn whose name matches the file's basename.
        fn = getattr(mod, provider, None)
        if fn is None or not inspect.iscoroutinefunction(fn):
            continue
        found.append((provider, fn))
    return found


_MODULES_CACHE: list[tuple[str, Any]] | None = None


def _modules() -> list[tuple[str, Any]]:
    global _MODULES_CACHE
    if _MODULES_CACHE is None:
        _MODULES_CACHE = _discover_modules()
    return _MODULES_CACHE


async def holehe_check(input: dict[str, Any]) -> dict[str, Any]:
    email = (input.get("email") or "").strip()
    if not email or "@" not in email:
        raise ValueError("input.email must be a valid email address")
    timeout_s = float(input.get("timeout_seconds", 30))
    only = input.get("only")  # optional list[str] of provider names

    modules = _modules()
    if isinstance(only, list) and only:
        wanted = {n.lower() for n in only}
        modules = [(n, fn) for n, fn in modules if n.lower() in wanted]

    start = time.perf_counter()
    async with httpx.AsyncClient(timeout=10.0) as client:
        async def run_one(name: str, fn: Any) -> dict[str, Any]:
            out: list[dict[str, Any]] = []
            try:
                await asyncio.wait_for(fn(email, client, out), timeout=timeout_s)
                if out:
                    r = out[0]
                    return {
                        "site": name,
                        "exists": bool(r.get("exists")),
                        "rate_limit": bool(r.get("rateLimit")),
                        "phone": r.get("phoneNumber") or None,
                        "fullname": r.get("fullName") or None,
                        "others": {k: v for k, v in r.items() if k not in {"name","domain","method","frequent_rate_limit","exists","rateLimit","phoneNumber","fullName"}},
                    }
                return {"site": name, "exists": False, "rate_limit": False}
            except asyncio.TimeoutError:
                return {"site": name, "exists": False, "rate_limit": False, "error": "timeout"}
            except Exception as e:
                return {"site": name, "exists": False, "rate_limit": False, "error": str(e)[:120]}

        results = await asyncio.gather(*(run_one(n, fn) for n, fn in modules))

    confirmed = [r for r in results if r.get("exists")]
    rate_limited = [r for r in results if r.get("rate_limit")]
    errors = [r for r in results if r.get("error")]

    return {
        "email": email,
        "checked": len(modules),
        "confirmed_count": len(confirmed),
        "confirmed": confirmed,
        "rate_limited_count": len(rate_limited),
        "rate_limited": [r["site"] for r in rate_limited],
        "errors_count": len(errors),
        "took_ms": int((time.perf_counter() - start) * 1000),
        "note": "false negatives are common — many sites have patched the detection patterns since holehe's last update",
    }
