"""Authentication for trusted backend-to-RAG calls."""

import hmac
import os


def service_token(token=None):
    token = os.environ.get("RAG_SERVICE_TOKEN", "") if token is None else token
    if not token or not token.isascii() or any(c.isspace() or ord(c) < 33 for c in token):
        raise ValueError("RAG_SERVICE_TOKEN must be a nonempty ASCII token")
    return token


def authorized(value, token):
    return isinstance(value, str) and hmac.compare_digest(value.encode("utf-8"), ("Bearer " + token).encode("ascii"))
