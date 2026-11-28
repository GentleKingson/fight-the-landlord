#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export UV_PROJECT_ENVIRONMENT="${UV_PROJECT_ENVIRONMENT:-$PWD/douzero/.venv}"
uv sync --project douzero --locked --no-dev --group build --python "${PYTHON:-3.12}"
python_bin="$UV_PROJECT_ENVIRONMENT/bin/python"
marvel_dir="${MARVEL_ROOT:-$PWD/douzero/vendor/doudizhu}"
marvel_revision=acdcf199cf422e2b683cb22142b8e7cb33b14460
if [ ! -d "$marvel_dir/.git" ]; then
  git clone https://github.com/GentleKingson/doudizhu.git "$marvel_dir"
fi
if [ -n "$(git -C "$marvel_dir" status --porcelain)" ]; then
  echo "MARVEL source has local changes; use a clean MARVEL_ROOT" >&2
  exit 1
fi
git -C "$marvel_dir" fetch origin "$marvel_revision"
git -C "$marvel_dir" checkout --detach "$marvel_revision"
# Make pybind11 discoverable by upstream's CMake build.
export CMAKE_PREFIX_PATH="$("$python_bin" -m pybind11 --cmakedir)${CMAKE_PREFIX_PATH:+:$CMAKE_PREFIX_PATH}"
export NANOPOKERLIB_BUILD_JOBS="${NANOPOKERLIB_BUILD_JOBS:-2}"
uv pip install --python "$python_bin" --no-deps --no-build-isolation "$marvel_dir/nanopokerlib"
echo "Ready. MARVEL_ROOT=$marvel_dir $python_bin douzero/server.py"
