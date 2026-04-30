"""
google_trends_lookup — interest-over-time + interest-by-region + related queries
for one or more search terms via Google Trends (unofficial API).

Free, no key. Uses pytrends library which wraps Google's internal Trends
endpoints. CAVEAT: the unofficial API is fragile — Google can rate-limit or
break the protocol at any time. We catch errors gracefully.

Use cases:
  - Brand monitoring: trend strength of "Anthropic" vs "OpenAI" vs "Google AI"
  - Geographic intel: which countries search for a brand most
  - Discovery: related queries reveal what people associate with the brand
  - Threat actor tracking: search patterns around exploit names / leaked tools
"""
from __future__ import annotations

import time
from typing import Any


def _safe_to_dict(df: Any) -> list[dict]:
    """Convert a pandas DataFrame (or None) to a list-of-dicts safely."""
    if df is None:
        return []
    try:
        # Reset index so date becomes a column, then to_dict
        if hasattr(df, "reset_index"):
            df = df.reset_index()
        if hasattr(df, "to_dict"):
            records = df.to_dict(orient="records")
            # Convert any pandas/numpy types to Python natives
            cleaned = []
            for r in records:
                clean = {}
                for k, v in r.items():
                    if hasattr(v, "isoformat"):  # Timestamp
                        clean[str(k)] = v.isoformat()
                    elif hasattr(v, "item"):  # numpy scalar
                        try:
                            clean[str(k)] = v.item()
                        except Exception:
                            clean[str(k)] = str(v)
                    else:
                        clean[str(k)] = v
                cleaned.append(clean)
            return cleaned
    except Exception:
        return []
    return []


async def google_trends_lookup(input: dict[str, Any]) -> dict[str, Any]:
    """
    Args:
      keywords: list[str] — up to 5 search terms (Google's hard cap)
      timeframe: str — e.g. "today 12-m", "today 5-y", "now 7-d", "all"
      geo: str — ISO country code or empty for worldwide
      include_related: bool — fetch related_queries (extra request, default True)
      include_regional: bool — fetch interest_by_region (extra request, default True)
    """
    keywords = input.get("keywords") or []
    if isinstance(keywords, str):
        keywords = [keywords]
    if not keywords:
        kw = input.get("keyword")
        if isinstance(kw, str) and kw.strip():
            keywords = [kw.strip()]
    if not keywords:
        return {"error": "input.keywords (list) or input.keyword (string) required"}
    if len(keywords) > 5:
        return {"error": "Google Trends caps at 5 keywords per request; trim list"}

    timeframe = input.get("timeframe", "today 12-m")
    geo = input.get("geo", "")
    include_related = bool(input.get("include_related", True))
    include_regional = bool(input.get("include_regional", True))

    started = time.time()
    output: dict[str, Any] = {
        "keywords": keywords,
        "timeframe": timeframe,
        "geo": geo or "WORLDWIDE",
        "source": "trends.google.com (unofficial)",
    }

    try:
        # urllib3 2.x removed `method_whitelist` in favor of `allowed_methods`.
        # pytrends 4.9.x still uses the old arg — monkey-patch before import.
        import urllib3.util.retry as _retry_module  # type: ignore
        _orig_init = _retry_module.Retry.__init__
        def _patched_init(self, *args, **kwargs):
            if "method_whitelist" in kwargs:
                kwargs["allowed_methods"] = kwargs.pop("method_whitelist")
            return _orig_init(self, *args, **kwargs)
        _retry_module.Retry.__init__ = _patched_init  # type: ignore
        # Lazy import — only load pytrends when this tool is called
        from pytrends.request import TrendReq  # type: ignore
    except ImportError:
        output["error"] = "pytrends not installed — run `uv sync` in apps/py-worker"
        output["tookMs"] = int((time.time() - started) * 1000)
        return output

    try:
        # hl=en-US, tz=240 = US Eastern offset; default tz works for most cases
        pytrends = TrendReq(
            hl="en-US",
            tz=240,
            timeout=(10, 25),
            retries=2,
            backoff_factor=0.3,
        )
        pytrends.build_payload(
            kw_list=keywords,
            cat=0,
            timeframe=timeframe,
            geo=geo,
            gprop="",
        )

        # Interest over time
        try:
            iot = pytrends.interest_over_time()
            output["interest_over_time"] = _safe_to_dict(iot)
        except Exception as e:
            output["interest_over_time_error"] = str(e)[:200]

        # Interest by region
        if include_regional:
            try:
                ibr = pytrends.interest_by_region(
                    resolution="COUNTRY" if not geo else "REGION",
                    inc_low_vol=False,
                    inc_geo_code=True,
                )
                # Sort/cap to top 30 regions per keyword for response size
                regional = _safe_to_dict(ibr)
                # Keep top 30 by sum across all keywords
                if len(regional) > 30:
                    def total(r):
                        return sum(int(r.get(k, 0) or 0) for k in keywords)
                    regional = sorted(regional, key=total, reverse=True)[:30]
                output["interest_by_region_top30"] = regional
            except Exception as e:
                output["interest_by_region_error"] = str(e)[:200]

        # Related queries
        if include_related:
            try:
                rq = pytrends.related_queries()
                related: dict[str, Any] = {}
                for kw, payload in (rq or {}).items():
                    if not isinstance(payload, dict):
                        continue
                    related[kw] = {
                        "top": _safe_to_dict(payload.get("top"))[:15],
                        "rising": _safe_to_dict(payload.get("rising"))[:15],
                    }
                output["related_queries"] = related
            except Exception as e:
                output["related_queries_error"] = str(e)[:200]

        # Highlights
        highlights = []
        iot = output.get("interest_over_time", [])
        if iot:
            # Find peak per keyword
            for kw in keywords:
                values = [int(row.get(kw, 0) or 0) for row in iot if isinstance(row, dict)]
                if values:
                    peak = max(values)
                    avg = sum(values) / len(values)
                    highlights.append(
                        f"{kw}: peak={peak}, avg={avg:.1f}, latest={values[-1]} (across {len(values)} time points)"
                    )
        regional = output.get("interest_by_region_top30") or []
        if regional and keywords:
            top_geo = regional[0]
            country = top_geo.get("geoName") or top_geo.get("geoCode") or "?"
            score = top_geo.get(keywords[0], 0)
            highlights.append(f"top region for {keywords[0]}: {country} (score={score})")
        related = output.get("related_queries") or {}
        for kw, payload in related.items():
            if not isinstance(payload, dict):
                continue
            rising = payload.get("rising") or []
            if rising:
                top_rise = rising[0].get("query") if isinstance(rising[0], dict) else None
                if top_rise:
                    highlights.append(f"{kw} top rising query: '{top_rise}'")
        output["highlight_findings"] = highlights

    except Exception as e:
        output["error"] = f"pytrends call failed: {str(e)[:300]}"

    output["tookMs"] = int((time.time() - started) * 1000)
    return output
