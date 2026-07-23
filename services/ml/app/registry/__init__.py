"""Model registry package.

Two SEPARATE namespaces, and they must stay separate:

* :func:`get_registry` — the **fraud** champion (a trained LightGBM artifact, or
  ``heuristic`` in cold start).
* :func:`get_comment_registry` — the **comment** classifier, which is today a set
  of rules and reports ``heuristic-comments-v1``.

A rule must never borrow a trained model's version string. See
``app/registry/comments.py``.
"""

from app.registry.comments import (
    COMMENT_ARTIFACT_DIR_ENV,
    COMMENT_HEURISTIC_VERSION,
    CommentModelRegistry,
    get_comment_registry,
)
from app.registry.registry import ArtifactRef, ModelRegistry, get_registry

__all__ = [
    "COMMENT_ARTIFACT_DIR_ENV",
    "COMMENT_HEURISTIC_VERSION",
    "ArtifactRef",
    "CommentModelRegistry",
    "ModelRegistry",
    "get_comment_registry",
    "get_registry",
]
