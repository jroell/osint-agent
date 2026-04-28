from __future__ import annotations

import os
import time
from dataclasses import dataclass
from typing import Optional

from fastapi import HTTPException, Request
from nacl.exceptions import BadSignatureError
from nacl.signing import VerifyKey

CLOCK_SKEW_TOLERANCE_S = 60


@dataclass
class SigningConfig:
    verify_key: VerifyKey

    @classmethod
    def from_env(cls) -> "SigningConfig":
        pub_hex = os.environ.get("WORKER_PUBLIC_KEY_HEX")
        if not pub_hex:
            raise RuntimeError("WORKER_PUBLIC_KEY_HEX required")
        pub = bytes.fromhex(pub_hex)
        if len(pub) != 32:
            raise RuntimeError("WORKER_PUBLIC_KEY_HEX must be 32 bytes")
        return cls(verify_key=VerifyKey(pub))


async def require_signed_request(request: Request) -> bytes:
    """
    FastAPI dependency. Returns the raw body. Raises 401 on any failure.
    Caller must use the returned bytes for further parsing.
    """
    body = await request.body()
    ts = request.headers.get("x-osint-ts")
    sig_hex = request.headers.get("x-osint-sig")
    if not ts or not sig_hex:
        raise HTTPException(status_code=401, detail="missing signature headers")

    try:
        ts_int = int(ts)
    except ValueError:
        raise HTTPException(status_code=401, detail="bad ts")

    now = int(time.time())
    if abs(now - ts_int) > CLOCK_SKEW_TOLERANCE_S:
        raise HTTPException(status_code=401, detail="ts out of tolerance")

    try:
        sig = bytes.fromhex(sig_hex)
    except ValueError:
        raise HTTPException(status_code=401, detail="bad sig hex")

    cfg: Optional[SigningConfig] = request.app.state.signing_config
    if cfg is None:
        raise RuntimeError("signing_config not initialized")

    message = f"{ts}\n".encode() + body
    try:
        cfg.verify_key.verify(message, sig)
    except BadSignatureError:
        raise HTTPException(status_code=401, detail="sig verify failed")

    return body
