from __future__ import annotations

import json
import os
import uuid
from typing import Any, Callable

from fastapi import Depends, FastAPI, HTTPException
from pydantic import BaseModel

from py_worker.auth import SigningConfig, require_signed_request
from py_worker.tools.stealth_http import stealth_http


class ToolError(BaseModel):
    code: str
    message: str


class Telemetry(BaseModel):
    tookMs: int
    cacheHit: bool = False
    proxyUsed: str | None = None


class ToolResponse(BaseModel):
    requestId: str
    ok: bool
    output: dict | None = None
    error: ToolError | None = None
    telemetry: Telemetry


app = FastAPI(title="osint-py-worker", version="0.0.1")


@app.on_event("startup")
def _startup() -> None:
    app.state.signing_config = SigningConfig.from_env()


@app.get("/healthz")
def healthz() -> dict[str, str]:
    return {"ok": "true", "service": "py-worker"}


_TOOLS: dict[str, Callable[..., Any]] = {
    "stealth_http_fetch": stealth_http,
}


@app.post("/tool", response_model=ToolResponse)
async def tool(raw_body: bytes = Depends(require_signed_request)) -> ToolResponse:
    try:
        req = json.loads(raw_body)
    except json.JSONDecodeError as e:
        raise HTTPException(status_code=400, detail=f"bad json: {e}")

    tool_name = req.get("tool")
    input_payload = req.get("input", {})
    request_id = req.get("requestId", str(uuid.uuid4()))

    handler = _TOOLS.get(tool_name)
    if handler is None:
        return ToolResponse(
            requestId=request_id,
            ok=False,
            error=ToolError(code="unknown_tool", message=tool_name),
            telemetry=Telemetry(tookMs=0),
        )

    import time
    t0 = time.perf_counter()
    try:
        output = await handler(input_payload)
        return ToolResponse(
            requestId=request_id,
            ok=True,
            output=output,
            telemetry=Telemetry(tookMs=int((time.perf_counter() - t0) * 1000)),
        )
    except Exception as e:
        return ToolResponse(
            requestId=request_id,
            ok=False,
            error=ToolError(code="tool_failure", message=str(e)),
            telemetry=Telemetry(tookMs=int((time.perf_counter() - t0) * 1000)),
        )


def run() -> None:
    import uvicorn
    port = int(os.environ.get("PORT", "8082"))
    uvicorn.run(app, host="0.0.0.0", port=port)


if __name__ == "__main__":
    run()
