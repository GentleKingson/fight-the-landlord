import hashlib
import io
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import model_assets


class ModelAssetTests(unittest.TestCase):
    def test_downloads_and_atomically_installs_verified_model(self) -> None:
        content = b"audited-model"
        digest = hashlib.sha256(content).hexdigest()

        with tempfile.TemporaryDirectory() as temporary_dir:
            with mock.patch.object(model_assets, "MODEL_SHA256", {"landlord": digest}):
                with mock.patch.object(
                    model_assets.urllib.request,
                    "urlopen",
                    return_value=io.BytesIO(content),
                ):
                    model_assets.download_models(temporary_dir)

            destination = Path(temporary_dir) / "landlord.onnx"
            self.assertEqual(destination.read_bytes(), content)
            self.assertFalse(destination.with_suffix(".onnx.part").exists())

    def test_accepts_existing_model_only_after_verifying_it(self) -> None:
        content = b"audited-model"
        digest = hashlib.sha256(content).hexdigest()

        with tempfile.TemporaryDirectory() as temporary_dir:
            destination = Path(temporary_dir) / "landlord.onnx"
            destination.write_bytes(content)

            with mock.patch.object(model_assets, "MODEL_SHA256", {"landlord": digest}):
                with mock.patch.object(model_assets.urllib.request, "urlopen") as urlopen:
                    model_assets.download_models(temporary_dir)

            urlopen.assert_not_called()

    def test_rejects_existing_model_with_wrong_digest(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_dir:
            destination = Path(temporary_dir) / "landlord.onnx"
            destination.write_bytes(b"tampered")

            with mock.patch.object(model_assets, "MODEL_SHA256", {"landlord": "0" * 64}):
                with self.assertRaisesRegex(RuntimeError, "SHA-256 mismatch"):
                    model_assets.download_models(temporary_dir)

    def test_rejects_download_with_wrong_digest_without_installing_it(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_dir:
            with mock.patch.object(model_assets, "MODEL_SHA256", {"landlord": "0" * 64}):
                with mock.patch.object(
                    model_assets.urllib.request,
                    "urlopen",
                    return_value=io.BytesIO(b"tampered"),
                ):
                    with self.assertRaisesRegex(RuntimeError, "SHA-256 mismatch"):
                        model_assets.download_models(temporary_dir)

            destination = Path(temporary_dir) / "landlord.onnx"
            self.assertFalse(destination.exists())
            self.assertFalse(destination.with_suffix(".onnx.part").exists())


if __name__ == "__main__":
    unittest.main()
