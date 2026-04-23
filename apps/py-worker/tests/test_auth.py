import time

import pytest
from fastapi.testclient import TestClient
from nacl.signing import SigningKey

from py_worker.main import app


@pytest.fixture
def client(monkeypatch):
    sk = SigningKey.generate()
    pk_hex = sk.verify_key.encode().hex()
    monkeypatch.setenv("WORKER_PUBLIC_KEY_HEX", pk_hex)

    # Manually trigger startup since TestClient doesn't use the lifespan if we capture
    # the signing key outside.
    from py_worker.auth import SigningConfig
    app.state.signing_config = SigningConfig.from_env()

    with TestClient(app) as c:
        c.signing_key = sk  # type: ignore[attr-defined]
        yield c


def _sign(client: TestClient, body_bytes: bytes) -> tuple[str, str]:
    ts = str(int(time.time()))
    msg = f"{ts}\n".encode() + body_bytes
    sig = client.signing_key.sign(msg).signature  # type: ignore[attr-defined]
    return ts, sig.hex()


def test_healthz_unauthenticated(client):
    r = client.get("/healthz")
    assert r.status_code == 200
    assert r.json() == {"ok": "true", "service": "py-worker"}


def test_tool_requires_signature(client):
    r = client.post("/tool", json={"tool": "x"})
    assert r.status_code == 401


def test_tool_with_valid_sig_hits_unknown_tool_path(client):
    body = b'{"requestId":"x","tool":"does-not-exist","input":{}}'
    ts, sig = _sign(client, body)
    r = client.post(
        "/tool",
        content=body,
        headers={"x-osint-ts": ts, "x-osint-sig": sig, "content-type": "application/json"},
    )
    assert r.status_code == 200
    data = r.json()
    assert data["ok"] is False
    assert data["error"]["code"] == "unknown_tool"
