"""Supervised fraud-classifier training pipeline (the flywheel).

This package turns the admin dispute-review label export into a trained LightGBM
model — but only once real labels exist. Below the per-class data floor it trains
nothing and emits no artifact, so ``app.registry`` keeps reporting the honest
cold-start ``heuristic`` state. A model here only ever AUGMENTS the unsupervised
coordination-first models; it never fabricates ground truth, never zero-fills a
missing feature vector, and never fits on an empty label set.

No NEW coordination sub-signal may be added to the feature set until the
`product/research/fraud-detection-signals.md` §4 claims are re-verified and the §7
GDPR/DPDP + ToS sign-off for the cross-account commenter graph is in place.
"""
