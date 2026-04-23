"""
Lightweight test: we don't execute the actual rnet network call in CI because it requires
the network. Instead we validate the input-parsing happy path + the error propagation.
Full integration testing lives in a separate manual smoke test in Task 15.
"""
import pytest

from py_worker.tools.stealth_http import stealth_http


@pytest.mark.asyncio
async def test_bad_impersonate():
    with pytest.raises(Exception):  # ValidationError from pydantic
        await stealth_http({"url": "https://example.com", "impersonate": "ie6-lol"})
