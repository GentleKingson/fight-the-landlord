"""Exercise CI bootstrap failures without depending on external services."""

import os
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "install-ci-lint.sh"
VERSION = "v2.11.1"
MODULE = "github.com/golangci/golangci-lint/v2"

FAKE_GO = r'''#!/usr/bin/env python3
import os
from pathlib import Path
import sys

args = sys.argv[1:]
if args[:2] == ["version", "-m"]:
    content = Path(args[2]).read_text()
    version = content.splitlines()[1].removeprefix("# module-version: ")
    print("\tpath\tgithub.com/golangci/golangci-lint/v2/cmd/golangci-lint")
    print("\tmod\tgithub.com/golangci/golangci-lint/v2\t" + version)
elif args[0] == "install":
    log = Path(os.environ["INSTALL_LOG"])
    with log.open("a") as output:
        output.write(args[1] + "\n")
    count = len(log.read_text().splitlines())
    if count <= int(os.environ.get("FAIL_ATTEMPTS", "0")):
        print("simulated module proxy failure", file=sys.stderr)
        sys.exit(1)
    assert os.environ["GOWORK"] == "off"
    version = os.environ.get("BUILD_VERSION", args[1].split("@", 1)[1])
    binary = Path(os.environ["GOBIN"]) / "golangci-lint"
    binary.write_text("#!/bin/sh\n# module-version: " + version + "\necho lint-version\n")
    binary.chmod(0o755)
else:
    sys.exit("unexpected go command: " + repr(args))
'''


class InstallLintTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="ci lint ")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.fake_bin = self.root / "bin"
        self.fake_bin.mkdir()
        self.write_executable(self.fake_bin / "go", FAKE_GO)
        self.write_executable(self.fake_bin / "sleep", "#!/bin/sh\nexit 0\n")
        self.install_dir = self.root / "installed"
        self.log = self.root / "install.log"
        self.github_path = self.root / "github-path"
        self.env = {
            **os.environ,
            "PATH": f"{self.fake_bin}:{os.environ['PATH']}",
            "INSTALL_LOG": str(self.log),
            "GITHUB_PATH": str(self.github_path),
        }

    @staticmethod
    def write_executable(path, contents):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(textwrap.dedent(contents))
        path.chmod(0o755)

    def cached_binary(self, version=VERSION, exit_code=0):
        self.write_executable(
            self.install_dir / "golangci-lint",
            f"#!/bin/sh\n# module-version: {version}\nexit {exit_code}\n",
        )

    def run_installer(self, **extra_env):
        return subprocess.run(
            ["bash", str(SCRIPT), VERSION, str(self.install_dir)],
            env={**self.env, **extra_env},
            text=True,
            capture_output=True,
            timeout=10,
            check=False,
        )

    def attempts(self):
        return self.log.read_text().splitlines() if self.log.exists() else []

    def assert_success(self, result):
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.github_path.read_text(), f"{self.install_dir}\n")
        self.assertFalse(list(self.install_dir.glob(".install.*")))

    def test_cold_install_builds_exact_version(self):
        self.assert_success(self.run_installer())
        self.assertEqual(self.attempts(), [f"{MODULE}/cmd/golangci-lint@{VERSION}"])
        self.assertTrue((self.install_dir / "golangci-lint").is_file())

    def test_valid_cache_needs_no_network(self):
        self.cached_binary()
        self.assert_success(self.run_installer(FAIL_ATTEMPTS="99"))
        self.assertEqual(self.attempts(), [])

    def test_stale_cache_is_rebuilt(self):
        self.cached_binary(version="v2.10.0")
        self.assert_success(self.run_installer())
        self.assertEqual(len(self.attempts()), 1)
        self.assertIn(VERSION, (self.install_dir / "golangci-lint").read_text())

    def test_unusable_cache_is_rebuilt(self):
        self.cached_binary(exit_code=1)
        self.assert_success(self.run_installer())
        self.assertEqual(len(self.attempts()), 1)

    def test_transient_failure_recovers(self):
        self.assert_success(self.run_installer(FAIL_ATTEMPTS="2"))
        self.assertEqual(len(self.attempts()), 3)

    def test_persistent_failure_does_not_publish_a_tool(self):
        result = self.run_installer(FAIL_ATTEMPTS="99")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(self.attempts()), 3)
        self.assertFalse(self.github_path.exists())
        self.assertEqual(list(self.install_dir.iterdir()), [])

    def test_unexpected_build_version_is_rejected(self):
        result = self.run_installer(BUILD_VERSION="v2.10.0")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.github_path.exists())
        self.assertEqual(list(self.install_dir.iterdir()), [])


if __name__ == "__main__":
    unittest.main()
