"""
maigret_search — username enumeration across 3000+ sites using maigret
(forked from sherlock, actively maintained). Bellingcat documents this
as a primary recon tool.

Trade-offs:
  * Massive site list (~3000) means scanning everything takes minutes.
    We default to top_sites=50 for sub-30-second scans; pass top_sites=null
    to scan all sites. Maigret ranks sites by traffic so the top tail
    catches the high-signal hits.
  * Like all sherlock-family tools, it produces both false positives (sites
    that match too eagerly on the username pattern) and false negatives
    (sites whose detection patterns have rotted). Always manually verify hits.
"""
from __future__ import annotations

import logging
import os
import time
from typing import Any

from maigret import MaigretDatabase
from maigret.checking import maigret as maigret_search_fn
from maigret.notify import QueryNotify

# Quiet logger — maigret is chatty by default.
_logger = logging.getLogger("maigret-py-worker")
_logger.setLevel(logging.WARNING)


class _SilentNotify(QueryNotify):
    """No-op notifier so library logging doesn't pollute py-worker stdout."""
    def start(self, *a, **kw): pass
    def update(self, *a, **kw): pass
    def finish(self, *a, **kw): pass


_DB_CACHE: MaigretDatabase | None = None


def _load_db() -> MaigretDatabase:
    global _DB_CACHE
    if _DB_CACHE is not None:
        return _DB_CACHE
    # Maigret ships sites data under the package install dir.
    import maigret as _maigret_pkg
    base = os.path.dirname(_maigret_pkg.__file__)
    candidates = [
        os.path.join(base, "resources", "data.json"),
        os.path.join(base, "data.json"),
    ]
    db_path = next((p for p in candidates if os.path.exists(p)), None)
    if db_path is None:
        raise RuntimeError(
            f"could not locate maigret data.json under {base} (looked in resources/, root)"
        )
    db = MaigretDatabase().load_from_path(db_path)
    _DB_CACHE = db
    return db


async def maigret_search(input: dict[str, Any]) -> dict[str, Any]:
    username = (input.get("username") or "").strip()
    if not username:
        raise ValueError("input.username required")
    top_sites_in = input.get("top_sites", 50)
    top_sites = None if top_sites_in is None else int(top_sites_in)
    timeout_s = int(input.get("timeout_seconds", 25))

    db = _load_db()
    if top_sites is not None and top_sites > 0:
        sites = db.ranked_sites_dict(top=top_sites)
    else:
        sites = db.ranked_sites_dict()

    start = time.perf_counter()
    results = await maigret_search_fn(
        username=username,
        site_dict=sites,
        logger=_logger,
        query_notify=_SilentNotify(),
        timeout=timeout_s,
        is_parsing_enabled=False,
        max_connections=50,
        no_progressbar=True,
    )
    took_ms = int((time.perf_counter() - start) * 1000)

    # `results` is {site_name: {url_user, status: QueryStatus, ...}}
    found: list[dict[str, Any]] = []
    unknown: list[str] = []
    for site_name, info in results.items():
        status = info.get("status")
        # Maigret's QueryStatus enum has .CLAIMED / .AVAILABLE / .UNKNOWN / .ILLEGAL
        status_str = getattr(status, "name", str(status))
        if status_str == "CLAIMED":
            found.append({
                "site": site_name,
                "url": info.get("url_user", ""),
                "tags": info.get("tags", []),
            })
        elif status_str == "UNKNOWN":
            unknown.append(site_name)

    return {
        "username": username,
        "sites_checked": len(results),
        "found_count": len(found),
        "found": found,
        "unknown_count": len(unknown),
        "took_ms": took_ms,
        "source": f"maigret (top_sites={top_sites if top_sites else 'all'})",
        "note": "matches with status=CLAIMED are positive; UNKNOWN means the detection pattern was inconclusive (patched sites, captcha, etc.) — try increasing top_sites or manually checking the URLs",
    }
