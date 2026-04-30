"""
sherlock_search — original ~400-site username enumeration. Maigret is
more comprehensive, but Sherlock has the broadest community recognition
in bug-bounty and journalism workflows, so we expose it as a separate
tool with an identical interface.
"""
from __future__ import annotations

import asyncio
import io
import time
from contextlib import redirect_stdout
from typing import Any

from sherlock_project.sherlock import sherlock as _sherlock_run
from sherlock_project.notify import QueryNotify
from sherlock_project.result import QueryStatus
from sherlock_project.sites import SitesInformation


class _SilentNotify(QueryNotify):
    def start(self, *a, **kw): pass
    def update(self, *a, **kw): pass
    def finish(self, *a, **kw): pass


_SITES_CACHE: dict[str, dict[str, str]] | None = None


def _load_sites() -> dict[str, dict[str, str]]:
    global _SITES_CACHE
    if _SITES_CACHE is not None:
        return _SITES_CACHE
    info = SitesInformation()
    sites = {site.name: site.information for site in info}
    _SITES_CACHE = sites
    return sites


async def sherlock_search(input: dict[str, Any]) -> dict[str, Any]:
    username = (input.get("username") or "").strip()
    if not username:
        raise ValueError("input.username required")
    timeout_s = int(input.get("timeout_seconds", 60))

    def _run() -> dict[str, Any]:
        sites = _load_sites()
        start = time.perf_counter()
        # sherlock prints progress to stdout even with our silent notifier,
        # so capture-and-discard.
        with redirect_stdout(io.StringIO()):
            results = _sherlock_run(
                username=username,
                site_data=sites,
                query_notify=_SilentNotify(),
                tor=False,
                unique_tor=False,
                dump_response=False,
                proxy=None,
                timeout=timeout_s,
            )
        took_ms = int((time.perf_counter() - start) * 1000)

        found = []
        for site_name, payload in results.items():
            qr = payload.get("status") if isinstance(payload, dict) else None
            status = getattr(qr, "status", None) if qr else None
            if status == QueryStatus.CLAIMED:
                found.append({
                    "site": site_name,
                    "url": payload.get("url_user", ""),
                    "response_time_ms": int((qr.query_time or 0) * 1000) if qr else None,
                })
        return {
            "username": username,
            "sites_checked": len(results),
            "found_count": len(found),
            "found": found,
            "took_ms": took_ms,
            "source": "sherlock-project",
        }

    # sherlock's I/O is synchronous (FuturesSession-based). Run in a thread
    # so it doesn't block the FastAPI event loop.
    return await asyncio.to_thread(_run)
