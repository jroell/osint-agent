"""
ghunt_run — Google account enrichment via GHunt. Given an email, returns
public Google profile data: full name, profile photo, last-seen timestamp,
linked services (YouTube, Maps reviews, Photos), Google ID.

REQUIRES authenticated session: GHunt needs a logged-in Google session's
master_token + oauth_token to query the internal endpoints. The user must
either have run `ghunt login` once on this machine (creating
~/.malfrats/ghunt/creds.m) or provide the env vars below at call time.

Without creds, this tool returns a clear error explaining what to do.
"""
from __future__ import annotations

import asyncio
import json
import os
import shutil
import time
from typing import Any


GHUNT_CREDS_FILE = os.path.expanduser("~/.malfrats/ghunt/creds.m")


def _binary_path() -> str:
    cand = shutil.which("ghunt")
    if cand:
        return cand
    home_local = os.path.expanduser("~/.local/bin/ghunt")
    if os.path.exists(home_local):
        return home_local
    raise RuntimeError("ghunt not found on PATH. Install once: `uv tool install ghunt`")


def _have_creds() -> bool:
    return os.path.exists(GHUNT_CREDS_FILE)


async def ghunt_email(input: dict[str, Any]) -> dict[str, Any]:
    email = (input.get("email") or "").strip()
    if not email or "@" not in email:
        raise ValueError("input.email must be a valid email")
    timeout_s = int(input.get("timeout_seconds", 60))

    if not _have_creds():
        raise RuntimeError(
            "GHunt requires Google session credentials. Run `ghunt login` once "
            "on this machine to generate ~/.malfrats/ghunt/creds.m. You will be "
            "prompted to paste a master_token + oauth_token from a logged-in "
            "Google session (see https://github.com/mxrch/GHunt#authentication)."
        )

    binary = _binary_path()
    # `ghunt email <addr> --json -` writes JSON to stdout.
    cmd = [binary, "email", email, "--json", "-"]
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
        raise RuntimeError(f"ghunt timed out after {timeout_s}s")
    took_ms = int((time.perf_counter() - start) * 1000)

    if proc.returncode != 0:
        raise RuntimeError(
            f"ghunt exited {proc.returncode}: {stderr.decode('utf-8', errors='replace')[:500]}"
        )

    raw_text = stdout.decode("utf-8", errors="replace").strip()
    parsed: Any = None
    try:
        # ghunt prints a banner before JSON; find the first { and slice.
        first = raw_text.find("{")
        if first >= 0:
            parsed = json.loads(raw_text[first:])
    except json.JSONDecodeError:
        parsed = None

    return {
        "email": email,
        "took_ms": took_ms,
        "source": "ghunt",
        "data": parsed if parsed is not None else {"raw_stdout": raw_text[-2000:]},
        "note": "ghunt scrapes public Google account surfaces (Maps reviews, YouTube, Photos, Calendar) reachable from the email's Google ID. Empty result usually means the account hasn't been linked to those surfaces.",
    }
