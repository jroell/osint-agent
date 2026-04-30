"""
theharvester_run — multi-source domain/email/subdomain reconnaissance via
theHarvester. Runs as an isolated `uv tool` subprocess (the binary has
conflicting transitive deps with ghunt; both are installed separately
via `uv tool install`).

Default sources are FREE and need no API keys. Commercial sources
(bing/hunter/shodan/criminalip etc.) are accessible via the `sources` arg
once the user has provided keys via theHarvester's own ~/.theHarvester/api-keys.yaml.
"""
from __future__ import annotations

import asyncio
import json
import os
import shutil
import tempfile
import time
from typing import Any

# Free sources that don't require an API key. Vetted as of theHarvester 4.10.1
# (anubis was removed in 4.10.0; brave/duckduckgo/mojeek are search-engine
# enumerators that don't strictly need keys but can rate-limit).
DEFAULT_FREE_SOURCES = "crtsh,dnsdumpster,hackertarget,otx,rapiddns,threatcrowd,urlscan,subdomaincenter"


def _binary_path() -> str:
    # `uv tool install` puts the binary in ~/.local/bin by default.
    cand = shutil.which("theHarvester")
    if cand:
        return cand
    home_local = os.path.expanduser("~/.local/bin/theHarvester")
    if os.path.exists(home_local):
        return home_local
    raise RuntimeError(
        "theHarvester not found on PATH. Install once with: "
        "`uv tool install --from git+https://github.com/laramies/theHarvester.git theHarvester`"
    )


async def theharvester(input: dict[str, Any]) -> dict[str, Any]:
    domain = (input.get("domain") or "").strip()
    if not domain:
        raise ValueError("input.domain required")
    sources = (input.get("sources") or DEFAULT_FREE_SOURCES).strip()
    limit = int(input.get("limit", 100))
    timeout_s = int(input.get("timeout_seconds", 90))

    binary = _binary_path()

    with tempfile.TemporaryDirectory() as tmp:
        out_path = os.path.join(tmp, "out")
        cmd = [binary, "-d", domain, "-b", sources, "-l", str(limit), "-f", out_path]
        start = time.perf_counter()
        proc = await asyncio.create_subprocess_exec(
            *cmd,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        try:
            stdout, stderr = await asyncio.wait_for(proc.communicate(), timeout=timeout_s)
        except asyncio.TimeoutError:
            proc.kill()
            await proc.communicate()
            raise RuntimeError(f"theHarvester timed out after {timeout_s}s")

        took_ms = int((time.perf_counter() - start) * 1000)
        combined = (stdout.decode("utf-8", errors="replace") + "\n" + stderr.decode("utf-8", errors="replace"))
        # theHarvester writes <out>.json (and .xml/.html) — load the JSON.
        json_path = out_path + ".json"
        if proc.returncode != 0:
            raise RuntimeError(
                f"theHarvester exited {proc.returncode}: {combined[-600:]}"
            )
        # Even on rc=0, theHarvester sometimes prints "[!] Invalid source." to stdout
        # without producing the .json file. Surface that as a real error.
        if not os.path.exists(json_path):
            raise RuntimeError(
                f"theHarvester produced no output (rc={proc.returncode}). "
                f"Tail of stdout/stderr:\n{combined[-600:]}"
            )
        data: dict[str, Any] = {}
        with open(json_path, "r", encoding="utf-8") as f:
            try:
                data = json.load(f)
            except json.JSONDecodeError:
                data = {}

    return {
        "domain": domain,
        "sources": sources.split(","),
        "ips": data.get("ips", []),
        "hosts": data.get("hosts", []),
        "emails": data.get("emails", []),
        "asns": data.get("asns", []),
        "trello_urls": data.get("trello_urls", []),
        "linkedin_people": data.get("linkedin_people", []),
        "took_ms": took_ms,
        "source": "theHarvester",
        "note": "default sources are free; for bing/hunter/etc. populate ~/.theHarvester/api-keys.yaml and pass sources= explicitly",
    }
