"""Coordination detection via maximal cliques in the co-commenter graph.

Replaces the previous HDBSCAN pod detector. The research signal is concrete:
the **count of maximal cliques of size ≥ 5** in the shared-post-weighted
co-commenter graph separated suspicious from benign channels by three orders of
magnitude (12,241 / 9,246 / 782 vs 26 / 24 / 20; arXiv 2311.05791). The
secondary signal is the **clique-membership fraction** — the share of commenters
that sit inside any such clique.

Maximal-clique enumeration is worst-case exponential, so it is guarded, in
order: prune weak edges (in :mod:`app.graph.cocomment`) → reduce to the k-core
that can still contain a clique of the target size → hard-cap the node count fed
to the enumerator → hard time budget → hard cap on cliques materialised. Any
guard firing degrades the result to ``partial`` instead of hanging.

igraph's clique enumeration runs in C and cannot be interrupted mid-call, so the
time budget is enforced by bounding the graph *before* the call and by the
``max_results`` materialisation cap — a bounded graph is what makes the call
itself terminate quickly.
"""

from __future__ import annotations

import time
from dataclasses import dataclass

from app.graph.cocomment import build_cocomment_graph
from app.schemas import CommentEvent

# Wall-clock budget for one detection call. Generous enough for real channels,
# small enough that a pathological input cannot stall a request.
_TIME_BUDGET_SECONDS = 5.0

# Hard cap on the node count handed to the exponential enumerator. After the
# k-core reduction, if more vertices survive we keep the deepest-core ones.
_MAX_CLIQUE_NODES = 3_000

# Hard cap on cliques materialised. Hitting it means clique_count is a lower
# bound and the result is partial.
_MAX_CLIQUES = 200_000

# Cliques reported back (member lists). The count is exact; the listing is a
# top-slice by internal edge weight so the response stays small.
_MAX_PODS_RETURNED = 20

# Shared-post count at which a clique's mean edge weight saturates cohesion.
_COHESION_SATURATION = 10.0

# Unsupervised output is never fully trusted, so per-pod confidence is capped.
_CONFIDENCE_CAP = 0.6


@dataclass(frozen=True)
class PodResult:
    members: list[str]
    cohesion: float
    confidence: float


@dataclass(frozen=True)
class CliqueResult:
    pods: list[PodResult]
    clique_count: int
    membership_fraction: float
    commenters_analyzed: int
    partial: bool


def _mean_clique_weight(graph, vertices: list[int]) -> float:
    """Mean shared-post weight over the pairs inside one clique."""
    total = 0.0
    pairs = 0
    for a_i in range(len(vertices)):
        for b_i in range(a_i + 1, len(vertices)):
            eid = graph.get_eid(vertices[a_i], vertices[b_i], error=False)
            if eid >= 0:
                total += float(graph.es[eid]["weight"])
                pairs += 1
    return total / pairs if pairs else 0.0


def detect_cliques(
    events: list[CommentEvent],
    min_pod_size: int,
    min_shared_posts: int,
) -> CliqueResult:
    """Enumerate coordinated commenter cliques under a strict compute budget."""
    deadline = time.monotonic() + _TIME_BUDGET_SECONDS
    built = build_cocomment_graph(events, min_shared_posts, deadline)
    graph = built.graph
    total = built.total_commenters
    partial = built.partial

    if graph.vcount() == 0 or graph.ecount() == 0:
        return CliqueResult([], 0, 0.0, total, partial)

    # A clique of size k needs every member to have degree ≥ k-1 within it, so
    # all its members lie in the (k-1)-core. Reducing to that core preserves
    # every clique of the target size while shrinking the search.
    coreness = graph.coreness()
    keep = [v for v in range(graph.vcount()) if coreness[v] >= min_pod_size - 1]
    if not keep:
        return CliqueResult([], 0, 0.0, total, partial)

    if len(keep) > _MAX_CLIQUE_NODES:
        keep = sorted(keep, key=lambda v: (-coreness[v], graph.vs[v]["name"]))
        keep = sorted(keep[:_MAX_CLIQUE_NODES])
        partial = True

    core = graph.subgraph(keep)
    cliques = core.maximal_cliques(min=min_pod_size, max_results=_MAX_CLIQUES)
    clique_count = len(cliques)
    if clique_count >= _MAX_CLIQUES:
        partial = True

    if clique_count == 0:
        return CliqueResult([], 0, 0.0, total, partial)

    members_in_cliques: set[int] = set()
    for clique in cliques:
        members_in_cliques.update(clique)
    membership_fraction = len(members_in_cliques) / total if total else 0.0

    # Report the strongest cliques (highest mean shared-post weight) only.
    scored = sorted(
        (
            (_mean_clique_weight(core, list(clique)), clique)
            for clique in cliques
        ),
        key=lambda sc: (-sc[0], [core.vs[i]["name"] for i in sc[1]]),
    )
    pods: list[PodResult] = []
    for mean_weight, clique in scored[:_MAX_PODS_RETURNED]:
        cohesion = min(mean_weight / _COHESION_SATURATION, 1.0)
        size_factor = min(len(clique) / (2 * min_pod_size), 1.0)
        confidence = min(cohesion * size_factor, _CONFIDENCE_CAP)
        members = sorted(core.vs[i]["name"] for i in clique)
        pods.append(PodResult(members, cohesion, confidence))

    return CliqueResult(
        pods=pods,
        clique_count=clique_count,
        membership_fraction=min(membership_fraction, 1.0),
        commenters_analyzed=total,
        partial=partial,
    )
