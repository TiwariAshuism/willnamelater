"""Co-commenter graph construction from comment events.

Two commenters are joined by an edge weighted by the **number of posts they
both commented on** — not a binary co-occurrence flag. Shared-video weighting is
strictly more information than a binary edge and is the construction confirmed
3-0 across two independent papers (arXiv 1512.05457, arXiv 2311.05791). The
resulting weighted graph is what :mod:`app.models.cliques` enumerates cliques
over.

Building this graph is adversarially expensive: a single mega-thread with *k*
commenters implies O(k²) candidate edges, and a 50k-commenter channel must not
stall a request. Construction is therefore guarded at every step — a hard node
cap, a per-post fan-out cap, and a wall-clock deadline — and reports
``partial=True`` (with the graph it managed to build) rather than hanging.
"""

from __future__ import annotations

import time
from collections import Counter
from dataclasses import dataclass
from itertools import combinations

import igraph

from app.schemas import CommentEvent

# Hard cap on distinct commenters admitted to the graph. Above this we keep the
# most active commenters (coordinated accounts are, by construction, active) and
# flag the result partial.
MAX_GRAPH_NODES = 50_000

# Per-post fan-out cap. A post with more commenters than this is a viral
# mega-thread; enumerating all O(k²) pairs would dominate the budget, so we
# sample the most active participants on that post and flag partial.
MAX_POST_FANOUT = 2_000


@dataclass(frozen=True)
class CoGraph:
    """A built co-commenter graph plus the honesty flags from building it."""

    graph: igraph.Graph  # vertices carry a ``name``; edges carry a ``weight``
    total_commenters: int  # distinct commenters seen, before any node cap
    partial: bool  # true if any guard fired (node cap, fan-out cap, deadline)


def _kept_commenters(
    events: list[CommentEvent],
) -> tuple[dict[str, int], int, bool]:
    """Assign a stable index to each commenter admitted to the graph.

    Returns the ``commenter -> index`` map, the total distinct commenters seen,
    and whether the node cap fired. When the cap fires the most active
    commenters are kept, ties broken by name so the selection is deterministic.
    """
    activity = Counter(e.commenter for e in events)
    total = len(activity)
    if total <= MAX_GRAPH_NODES:
        names = sorted(activity)
        return {name: i for i, name in enumerate(names)}, total, False
    ranked = sorted(activity.items(), key=lambda kv: (-kv[1], kv[0]))
    names = sorted(name for name, _ in ranked[:MAX_GRAPH_NODES])
    return {name: i for i, name in enumerate(names)}, total, True


def build_cocomment_graph(
    events: list[CommentEvent],
    min_shared_posts: int,
    deadline: float,
) -> CoGraph:
    """Build the shared-post-weighted co-commenter graph under a time budget.

    ``deadline`` is a :func:`time.monotonic` timestamp after which construction
    stops early and returns what it has, marked partial. Edges with fewer than
    ``min_shared_posts`` shared posts are pruned — coordination shows up as
    *repeated* co-commenting, and pruning also blunts the large-fandom false
    positive where strangers share a single viral post.
    """
    index, total, partial = _kept_commenters(events)

    # Group commenter indices by post. A commenter counts once per post no
    # matter how many times they commented on it.
    per_post: dict[str, set[int]] = {}
    for event in events:
        idx = index.get(event.commenter)
        if idx is None:
            continue
        per_post.setdefault(event.post_id, set()).add(idx)

    pair_weight: dict[tuple[int, int], int] = {}
    for members in per_post.values():
        if time.monotonic() > deadline:
            partial = True
            break
        ordered = sorted(members)
        if len(ordered) > MAX_POST_FANOUT:
            ordered = ordered[:MAX_POST_FANOUT]
            partial = True
        for a, b in combinations(ordered, 2):
            pair_weight[(a, b)] = pair_weight.get((a, b), 0) + 1

    edges: list[tuple[int, int]] = []
    weights: list[int] = []
    for pair, weight in pair_weight.items():
        if weight >= min_shared_posts:
            edges.append(pair)
            weights.append(weight)

    n = len(index)
    graph = igraph.Graph(n=n)
    # Names index-aligned so vertex i is the commenter with index i.
    names = [""] * n
    for name, i in index.items():
        names[i] = name
    graph.vs["name"] = names
    if edges:
        graph.add_edges(edges)
        graph.es["weight"] = weights
    return CoGraph(graph=graph, total_commenters=total, partial=partial)
