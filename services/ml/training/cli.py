"""CLI: fetch labels, train if the floor is met, write the artifact.

    python -m training.cli --labels-url http://localhost:8080/v1/admin/training/labels \
        --token <admin-jwt> --out $INFLUAUDIT_ML_ARTIFACTS

Below the data floor it writes nothing and exits 0 — the service keeps serving
the cold-start models. Above it, it writes model + manifest into --out, which
the running ML service picks up on its next request (the registry reflects the
filesystem at call time).
"""

from __future__ import annotations

import argparse
import sys

from training.artifact import write_artifact
from training.labels import fetch_labels
from training.train import train


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        description="Train the supervised fraud classifier from the admin label export."
    )
    parser.add_argument(
        "--labels-url", required=True, help="GET .../v1/admin/training/labels"
    )
    parser.add_argument("--token", default=None, help="admin bearer token")
    parser.add_argument(
        "--out",
        required=True,
        help="artifact directory the ML service reads via INFLUAUDIT_ML_ARTIFACTS",
    )
    args = parser.parse_args(argv)

    labels = fetch_labels(args.labels_url, token=args.token)
    result = train(labels)
    if not result.trained:
        counts = result.counts
        print(
            f"below data floor: {counts['positive']} positive / "
            f"{counts['negative']} negative labelled examples, "
            f"need >= {counts['floor']} per class. No artifact written; "
            "the service keeps serving the cold-start models."
        )
        return 0

    manifest = write_artifact(
        args.out, result.model_bytes, metrics=result.metrics, counts=result.counts
    )
    print(
        f"wrote model {manifest['version']} to {args.out}; "
        f"val metrics: {result.metrics}"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
