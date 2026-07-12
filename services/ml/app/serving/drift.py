"""Lightweight prediction-drift signal over recent served scores.

Between retrains the input population shifts (a new campaign, a viral niche, a
bought-follower wave). A cheap, honest drift estimate lets an operator notice and
trigger an emergency retrain before the champion silently degrades.

This is deliberately minimal — no extra dependency beyond numpy (already a
runtime dep). It keeps a bounded ring of the most recent served fraud scores and
compares the newer half against the older half with the Population Stability
Index (PSI), the same statistic the shadow gate (G5) uses. It is **self-
referential and honest**: it invents no baseline distribution; it only reports
how much recent traffic has moved relative to slightly-older recent traffic, and
every reading is explicitly an estimate.
"""

from __future__ import annotations

import math
import threading
from collections import deque

import numpy as np

#: Ring capacity. Older observations fall off; drift is always "recent vs
#: less-recent", never against a frozen (and quickly stale) snapshot.
DEFAULT_CAPACITY = 1000
#: Minimum observations *per window* before a PSI can be reported honestly.
#: Below this the status is ``insufficient_data`` and psi is null.
MIN_PER_WINDOW = 50
#: Histogram bins for the PSI comparison.
DEFAULT_BINS = 10
#: PSI at/above which drift is flagged. 0.25 is the conventional "significant
#: shift" threshold and matches the shadow gate (RETRAINING_ARCHITECTURE §4 G5).
PSI_THRESHOLD = 0.25

STATUS_INSUFFICIENT = "insufficient_data"
STATUS_STABLE = "stable"
STATUS_DRIFT = "drift_warning"

_EPS = 1e-6


def _psi(reference: np.ndarray, current: np.ndarray, bins: int) -> float:
    """Population Stability Index between two 1-D samples.

    Bin edges are taken from the reference range; a degenerate reference (all
    equal) yields 0.0 drift by definition. Empty bins are floored by a small
    epsilon so the log ratio stays finite.
    """
    lo = float(reference.min())
    hi = float(reference.max())
    if hi <= lo:
        return 0.0
    edges = np.linspace(lo, hi, bins + 1)
    # Clip so out-of-range current values land in the edge bins rather than
    # being dropped — an excursion outside the old range IS drift.
    ref_hist, _ = np.histogram(np.clip(reference, lo, hi), bins=edges)
    cur_hist, _ = np.histogram(np.clip(current, lo, hi), bins=edges)
    ref_frac = ref_hist / max(ref_hist.sum(), 1)
    cur_frac = cur_hist / max(cur_hist.sum(), 1)
    ref_frac = np.clip(ref_frac, _EPS, None)
    cur_frac = np.clip(cur_frac, _EPS, None)
    return float(np.sum((cur_frac - ref_frac) * np.log(cur_frac / ref_frac)))


class DriftMonitor:
    """Thread-safe ring of recent scores with an on-demand PSI snapshot."""

    def __init__(
        self,
        capacity: int = DEFAULT_CAPACITY,
        *,
        bins: int = DEFAULT_BINS,
        min_per_window: int = MIN_PER_WINDOW,
        psi_threshold: float = PSI_THRESHOLD,
    ) -> None:
        self._values: deque[float] = deque(maxlen=capacity)
        self._bins = bins
        self._min_per_window = min_per_window
        self._psi_threshold = psi_threshold
        self._lock = threading.Lock()

    def record(self, value: float) -> None:
        """Append one served score. Non-finite values are ignored."""
        v = float(value)
        if not math.isfinite(v):
            return
        with self._lock:
            self._values.append(v)

    def reset(self) -> None:
        with self._lock:
            self._values.clear()

    def snapshot(self) -> dict:
        """Compute the current drift reading.

        Splits the ring in half (older = reference, newer = current). When either
        half is below ``min_per_window`` the reading is ``insufficient_data`` and
        ``psi`` is null — drift is never asserted on too little traffic.
        """
        with self._lock:
            values = list(self._values)
        count = len(values)
        base = {
            "sample_count": count,
            "min_per_window": self._min_per_window,
            "psi_threshold": self._psi_threshold,
            "estimate": True,
        }
        half = count // 2
        if half < self._min_per_window:
            return {
                **base,
                "status": STATUS_INSUFFICIENT,
                "psi": None,
                "reference_count": half,
                "current_count": count - half,
                "detail": (
                    "Not enough recent traffic to estimate drift; "
                    f"need >= {self._min_per_window} scores per window."
                ),
            }
        arr = np.asarray(values, dtype=float)
        reference = arr[:half]
        current = arr[half:]
        psi = round(_psi(reference, current, self._bins), 6)
        drifted = psi >= self._psi_threshold
        return {
            **base,
            "status": STATUS_DRIFT if drifted else STATUS_STABLE,
            "psi": psi,
            "reference_count": int(reference.size),
            "current_count": int(current.size),
            "detail": (
                "Estimated population shift of recent served scores vs "
                "less-recent scores (PSI). An estimate, not a measurement."
            ),
        }

    def status(self) -> str:
        """Just the status string (for the health probe)."""
        return self.snapshot()["status"]


_default_monitor = DriftMonitor()


def get_drift_monitor() -> DriftMonitor:
    """Return the process-wide drift monitor."""
    return _default_monitor
