from app.registry.registry import HEURISTIC_VERSION, ModelRegistry
from training.artifact import MODEL_FILENAME, write_artifact


def test_written_artifact_is_loadable_by_registry(tmp_path):
    # The whole point of the pipeline: what it writes must flip the registry off
    # the heuristic path and report the model's version.
    manifest = write_artifact(
        tmp_path, b"model-bytes-here", metrics={"n_val": 5}, counts={"positive": 60}
    )
    reg = ModelRegistry(tmp_path)
    assert reg.is_supervised() is True
    assert reg.active_version() == manifest["version"]
    assert (tmp_path / MODEL_FILENAME).is_file()


def test_registry_stays_heuristic_without_an_artifact(tmp_path):
    # An empty artifact dir (the cold-start reality) must keep the honest
    # heuristic state — the training pipeline writing nothing is a valid outcome.
    reg = ModelRegistry(tmp_path)
    assert reg.is_supervised() is False
    assert reg.active_version() == HEURISTIC_VERSION


def test_version_is_deterministic_in_model_bytes(tmp_path):
    v1 = write_artifact(tmp_path / "a", b"same-model")["version"]
    v2 = write_artifact(tmp_path / "b", b"same-model")["version"]
    assert v1 == v2
