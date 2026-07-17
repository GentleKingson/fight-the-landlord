"""Download and verify the immutable DouZero ONNX model assets."""

from __future__ import annotations

import hashlib
import os
import shutil
import urllib.request
from pathlib import Path


# Hugging Face repository commit returned by the Hub API for the audited model
# revision. The digests are the repository LFS SHA-256 OIDs, independently
# verified against the downloaded bytes on 2026-07-17.
HF_REVISION = "57b3914046c2a0877016b8b8830fd07cf5b0ba08"
HF_BASE = (
    "https://huggingface.co/palemoky/douzero-baselines/resolve/"
    f"{HF_REVISION}/models_onnx/douzero_WP"
)
MODEL_SHA256 = {
    "landlord": "5ba2f1bb414483a3ecdf0d000159c1322b02e2d096e6d83be8a18765b56d6d32",
    "landlord_down": "9b816457615f8007dd3a657dde1f78dac4fec7c3b0041f6f89321a1c6335cbd1",
    "landlord_up": "244ec62a549a0141c6532dffd02bee7675621d095cb194362b47c8a923c95883",
}


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as model:
        for chunk in iter(lambda: model.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _verify(path: Path, expected: str) -> None:
    actual = _sha256(path)
    if actual != expected:
        raise RuntimeError(
            f"SHA-256 mismatch for {path.name}: expected {expected}, got {actual}"
        )


def download_models(model_dir: str | os.PathLike[str]) -> None:
    """Ensure all pinned models exist and match their audited SHA-256 values."""

    destination = Path(model_dir)
    destination.mkdir(parents=True, exist_ok=True)

    for position, expected in MODEL_SHA256.items():
        path = destination / f"{position}.onnx"
        if path.exists():
            _verify(path, expected)
            print(f"  Verified {path.name}")
            continue

        temporary = path.with_suffix(".onnx.part")
        try:
            print(f"  Downloading {path.name} from revision {HF_REVISION} ...")
            with urllib.request.urlopen(f"{HF_BASE}/{path.name}", timeout=60) as response:
                with temporary.open("wb") as model:
                    shutil.copyfileobj(response, model)
            _verify(temporary, expected)
            os.replace(temporary, path)
            print(f"  Verified {path.name}")
        finally:
            temporary.unlink(missing_ok=True)
