"""Models: per-call unsupervised estimators and the heuristic composite.

Nothing here loads a pretrained artifact. The IsolationForest and HDBSCAN
estimators are fitted fresh on each request's own data, which is the only
principled thing to do with no labeled history.
"""
