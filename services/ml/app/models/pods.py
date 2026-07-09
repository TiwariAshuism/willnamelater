"""Engagement-pod detection via density clustering of commenters.

Pods are groups of accounts that comment on the same posts together far more
often than independent audiences would. With no labels, we cluster the
commenter co-occurrence graph: HDBSCAN over a precomputed distance matrix finds
dense groups without being told how many to expect, and leaves everyone else as
noise. The clustering is deterministic for a given matrix.
"""

from __future__ import annotations

from dataclasses import dataclass

import numpy as np
from sklearn.cluster import HDBSCAN

from app.features.comments import cooccurrence_matrix
from app.schemas import CommentEvent

# Confidence ceiling: even a tight cluster is an unlabeled estimate.
_CONFIDENCE_CAP = 0.6


@dataclass(frozen=True)
class PodResult:
    members: list[str]
    cohesion: float
    confidence: float


def _distance_matrix(matrix: np.ndarray) -> np.ndarray:
    """Convert co-occurrence counts to a [0, 1] distance matrix.

    Similarity between two commenters is the share of the less-active one's
    comments that overlap with the other; distance is one minus that. This
    normalization keeps a highly active commenter from looking close to
    everyone merely by volume.
    """
    activity = np.diag(matrix).copy()
    n = matrix.shape[0]
    distance = np.ones((n, n), dtype=float)
    for i in range(n):
        distance[i, i] = 0.0
        for j in range(i + 1, n):
            denom = min(activity[i], activity[j])
            similarity = matrix[i, j] / denom if denom > 0 else 0.0
            similarity = min(similarity, 1.0)
            distance[i, j] = distance[j, i] = 1.0 - similarity
    return distance


def _cohesion(members_idx: list[int], distance: np.ndarray) -> float:
    """Mean intra-pod similarity (1 - distance) over member pairs."""
    if len(members_idx) < 2:
        return 0.0
    sims: list[float] = []
    for a in range(len(members_idx)):
        for b in range(a + 1, len(members_idx)):
            sims.append(1.0 - distance[members_idx[a], members_idx[b]])
    return float(np.mean(sims))


def detect_pods(
    events: list[CommentEvent], window_minutes: int, min_pod_size: int
) -> tuple[list[PodResult], int]:
    """Cluster commenters into pods.

    Returns the detected pods and the number of distinct commenters analyzed.
    """
    commenters, counts = cooccurrence_matrix(events, window_minutes)
    n = len(commenters)
    if n < min_pod_size:
        return [], n

    distance = _distance_matrix(counts)
    clusterer = HDBSCAN(
        min_cluster_size=min_pod_size,
        metric="precomputed",
        allow_single_cluster=True,
        copy=True,  # do not mutate the caller's distance matrix in place
    )
    labels = clusterer.fit_predict(distance)

    pods: list[PodResult] = []
    for label in sorted({int(x) for x in labels if x >= 0}):
        idx = [i for i, x in enumerate(labels) if int(x) == label]
        cohesion = _cohesion(idx, distance)
        # Confidence grows with cohesion and size past the minimum, capped.
        size_factor = min(len(idx) / (2 * min_pod_size), 1.0)
        confidence = min(cohesion * size_factor, _CONFIDENCE_CAP)
        pods.append(
            PodResult(
                members=[commenters[i] for i in idx],
                cohesion=cohesion,
                confidence=confidence,
            )
        )

    pods.sort(key=lambda p: (p.cohesion, len(p.members)), reverse=True)
    return pods, n
