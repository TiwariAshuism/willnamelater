"""Feature extraction: turn request payloads into numeric signals.

These functions are pure — no I/O, no model state — so they are trivially
deterministic and directly unit-testable, which is what lets the test suite
prove properties (boundedness, monotonicity) without any labeled data.
"""
