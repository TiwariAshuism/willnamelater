"""Models: coordination detection, the per-account tie-breaker, and the composite.

Nothing here loads a pretrained artifact. Coordination is measured structurally
(maximal cliques in the per-request co-commenter graph, :mod:`cliques`), the
per-account UnDBot metrics (:mod:`undbot`) are pure functions of the request, and
the composite (:mod:`heuristics`) blends them — the only principled thing to do
with no labeled history.
"""
