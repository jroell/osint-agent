"""
pdf_document_analyze — fetch a public PDF and extract metadata + text.

For OSINT this is the "what does this leaked PDF actually contain" tool:
metadata (author, creator, title, dates), embedded fonts, page count, and
the extracted text body so an LLM can reason over it. Returns truncated
text by default; pass full=true for the entire document (subject to a
hard 5 MiB cap to keep the JSON response sane).
"""
from __future__ import annotations

import io
import time
from typing import Any

import httpx
from pypdf import PdfReader


_TEXT_CHAR_CAP = 50_000
_FULL_TEXT_HARD_CAP = 5_000_000


async def pdf_document_analyze(input: dict[str, Any]) -> dict[str, Any]:
    url = (input.get("url") or "").strip()
    if not url:
        raise ValueError("input.url required (PDF URL)")
    full = bool(input.get("full", False))
    timeout_s = int(input.get("timeout_seconds", 30))

    start = time.perf_counter()
    async with httpx.AsyncClient(timeout=timeout_s, follow_redirects=True) as client:
        resp = await client.get(url, headers={
            "User-Agent": "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)",
            "Accept": "application/pdf",
        })
        resp.raise_for_status()
        data = resp.content

    if len(data) < 4 or not data.startswith(b"%PDF-"):
        raise ValueError(f"response did not start with %PDF- magic bytes (got {len(data)} bytes)")

    reader = PdfReader(io.BytesIO(data))
    meta_raw = reader.metadata or {}
    metadata: dict[str, Any] = {}
    for k, v in meta_raw.items():
        try:
            metadata[str(k).lstrip("/")] = str(v)
        except Exception:
            metadata[str(k).lstrip("/")] = repr(v)

    pages_text: list[str] = []
    total_chars = 0
    char_cap = _FULL_TEXT_HARD_CAP if full else _TEXT_CHAR_CAP
    truncated = False
    for i, page in enumerate(reader.pages):
        try:
            t = page.extract_text() or ""
        except Exception:
            t = ""
        if total_chars + len(t) > char_cap:
            t = t[: max(0, char_cap - total_chars)]
            truncated = True
        pages_text.append(t)
        total_chars += len(t)
        if total_chars >= char_cap:
            break

    is_encrypted = reader.is_encrypted
    return {
        "url": url,
        "size_bytes": len(data),
        "pages": len(reader.pages),
        "encrypted": is_encrypted,
        "metadata": metadata,
        "text_chars_returned": total_chars,
        "text_truncated": truncated,
        "text": "\n\n".join(pages_text),
        "took_ms": int((time.perf_counter() - start) * 1000),
        "source": "pypdf",
    }
