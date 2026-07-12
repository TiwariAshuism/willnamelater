"""Registry namespace for the comment classifier — DELIBERATELY NOT the fraud one.

The comment endpoint used to stamp its responses with
``app.registry.get_registry().active_version()`` — the **fraud** registry. That
registry reports the version of the promoted fraud champion (a LightGBM
classifier). The comment path is not that model and never was: it is an
18-phrase English frozenset plus two regexes (``app.features.comments``).

The failure mode that produced this module: the day a fraud champion is promoted
to ``$INFLUAUDIT_ML_ARTIFACTS/manifest.json``, the *comment* response would start
reporting e.g. ``fraud-lgbm-2026-07-01`` — printing a regex under a trained
model's version into a paying customer's PDF. Two different estimators MUST NOT
share a version namespace.

So the comment classifier gets its own slot:

* its own env var — ``INFLUAUDIT_COMMENT_ARTIFACTS`` — which is *never* defaulted
  to the fraud artifact dir. If it is unset there is simply no comment artifact.
* its own cold-start version — :data:`COMMENT_HEURISTIC_VERSION` — which is a
  truthful name (``heuristic-comments-v1``: rules, version 1), not ``heuristic``
  (already taken by the fraud path) and not a fraud model's version.

Until a real, trained, *evaluated* comment artifact is dropped into that
directory, :meth:`CommentModelRegistry.active_version` returns the hardcoded
heuristic version. Nothing that happens in the fraud artifact directory can
change it.
"""

from __future__ import annotations

import os
from pathlib import Path

from app.registry.registry import ArtifactRef, ModelRegistry

#: Version reported while the comment path is rules, not a model. Distinct from
#: the fraud registry's ``heuristic`` so the two can never be confused in a log,
#: a report, or a customer PDF.
COMMENT_HEURISTIC_VERSION = "heuristic-comments-v1"

#: Environment variable pointing at a directory holding a *comment* model
#: artifact. Separate from ``INFLUAUDIT_ML_ARTIFACTS`` (fraud) on purpose.
COMMENT_ARTIFACT_DIR_ENV = "INFLUAUDIT_COMMENT_ARTIFACTS"


class CommentModelRegistry:
    """Resolves the active *comment* model — and only the comment model.

    Composition, not inheritance, over :class:`ModelRegistry`: subclassing it
    would inherit its ``INFLUAUDIT_ML_ARTIFACTS`` fallback, which is exactly the
    leak this module exists to prevent.
    """

    def __init__(self, artifact_dir: Path | None = None) -> None:
        self._explicit_dir = artifact_dir

    def _dir(self) -> Path | None:
        if self._explicit_dir is not None:
            return self._explicit_dir
        env = os.environ.get(COMMENT_ARTIFACT_DIR_ENV)
        return Path(env) if env else None

    def active_ref(self) -> ArtifactRef | None:
        """The resolved comment artifact, or ``None`` (the current reality)."""
        directory = self._dir()
        if directory is None:
            return None
        # Artifact *validation* (manifest + model file on disk) is generic, so it
        # is reused. Only the directory it is pointed at differs.
        return ModelRegistry(artifact_dir=directory).active_ref()

    def active_version(self) -> str:
        """The comment model version, or the hardcoded heuristic version.

        Never consults the fraud registry, so promoting a fraud champion cannot
        move this string.
        """
        ref = self.active_ref()
        return ref.version if ref is not None else COMMENT_HEURISTIC_VERSION

    def is_supervised(self) -> bool:
        """True only once a real trained *comment* artifact exists. Today: false."""
        return self.active_ref() is not None


_default_comment_registry = CommentModelRegistry()


def get_comment_registry() -> CommentModelRegistry:
    """Return the process-wide comment registry."""
    return _default_comment_registry
