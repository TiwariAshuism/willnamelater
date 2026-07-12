"""Serving-time machinery layered on top of the cold-start models.

Nothing here fabricates a model or a label. These modules only activate when the
registry resolves a *real* trained artifact:

* :mod:`supervised` runs the champion (and, in a shadow window, the challenger)
  LightGBM model on the frozen feature vector.
* :mod:`shadow` fires the best-effort challenger-vs-champion log to the backend
  prediction-ingest endpoint — never shown to a user.
* :mod:`drift` keeps a lightweight population-stability estimate over recent
  served predictions so an operator can trigger an emergency retrain.
"""
