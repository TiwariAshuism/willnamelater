"""Prediction-drift monitor on synthetic inputs.

The scores here are fixtures chosen to drive the PSI computation across its three
regimes; they are not claimed measurements of any account.
"""

from __future__ import annotations

from app.serving.drift import (
    MIN_PER_WINDOW,
    PSI_THRESHOLD,
    STATUS_DRIFT,
    STATUS_INSUFFICIENT,
    STATUS_STABLE,
    DriftMonitor,
)


def test_insufficient_data_reports_null_psi() -> None:
    monitor = DriftMonitor()
    for _ in range(2 * MIN_PER_WINDOW - 2):
        monitor.record(50.0)
    snap = monitor.snapshot()
    assert snap["status"] == STATUS_INSUFFICIENT
    assert snap["psi"] is None
    assert snap["estimate"] is True


def test_stable_distribution_is_below_threshold() -> None:
    monitor = DriftMonitor()
    # Both halves draw from the same repeating pattern => near-zero PSI.
    for i in range(4 * MIN_PER_WINDOW):
        monitor.record(40.0 + (i % 20))
    snap = monitor.snapshot()
    assert snap["status"] == STATUS_STABLE
    assert snap["psi"] < PSI_THRESHOLD
    assert snap["reference_count"] >= MIN_PER_WINDOW
    assert snap["current_count"] >= MIN_PER_WINDOW


def test_shifted_distribution_flags_drift() -> None:
    monitor = DriftMonitor()
    # Older half clustered low, newer half clustered high => large PSI.
    for i in range(2 * MIN_PER_WINDOW):
        monitor.record(10.0 + (i % 5))
    for i in range(2 * MIN_PER_WINDOW):
        monitor.record(90.0 + (i % 5))
    snap = monitor.snapshot()
    assert snap["status"] == STATUS_DRIFT
    assert snap["psi"] >= PSI_THRESHOLD


def test_non_finite_values_are_ignored() -> None:
    monitor = DriftMonitor()
    monitor.record(float("nan"))
    monitor.record(float("inf"))
    monitor.record(float("-inf"))
    assert monitor.snapshot()["sample_count"] == 0


def test_reset_clears_the_ring() -> None:
    monitor = DriftMonitor()
    for _ in range(10):
        monitor.record(1.0)
    monitor.reset()
    assert monitor.snapshot()["sample_count"] == 0
