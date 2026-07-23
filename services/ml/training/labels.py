"""Load the admin dispute-review label export.

The labels are the ground truth: an admin resolving a dispute as *rejected*
(fraud flag stands) is a positive example; *upheld* (false positive) is a
negative one. They are read from the Go admin module's export endpoint,
GET /v1/admin/training/labels (LabelExportResponse).
"""

from __future__ import annotations

import json
import urllib.request


def parse_export(payload) -> list[dict]:
    """Extract the labels list from a LabelExportResponse dict."""
    return payload.get("labels") or []


def fetch_labels(
    url: str, *, token: str | None = None, timeout: int = 30
) -> list[dict]:
    """GET the admin label export and return its labels list. The endpoint is
    admin-only, so a bearer token is normally required."""
    request = urllib.request.Request(url)
    request.add_header("Accept", "application/json")
    if token:
        request.add_header("Authorization", f"Bearer {token}")
    with urllib.request.urlopen(request, timeout=timeout) as resp:
        payload = json.loads(resp.read().decode("utf-8"))
    return parse_export(payload)
