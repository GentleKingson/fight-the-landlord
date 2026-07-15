ARG PYTHON_BUILDER_IMAGE=dhi.io/python:3.14-dev@sha256:8eb7494ef9c51643958a11c386bd4fa3866d899c29c9cb4cf0e372a58ef7f023
ARG PYTHON_RUNTIME_IMAGE=dhi.io/python:3.14@sha256:700be1a5996abb46d16a1fca6868c7f3bd7b87c4f7c09477d74312caea035305
ARG UV_IMAGE=ghcr.io/astral-sh/uv:latest@sha256:0f36cb9361a3346885ca3677e3767016687b5a170c1a6b88465ec14aefec90aa

# 构建阶段：dev 变体带 shell / python，用于装依赖与下载模型。
# 基础镜像默认锁定多架构 index digest，升级需显式审核并更新。
FROM ${PYTHON_BUILDER_IMAGE} AS builder

# dev 变体默认非 root，构建阶段切到 root 以便写入 /usr/local/bin 与 /app
USER root

WORKDIR /app

# 从官方镜像取 uv 二进制（DHI python 不自带 uv）
COPY --from=${UV_IMAGE} /uv /usr/local/bin/uv

# 安装依赖到 .venv
COPY pyproject.toml uv.lock* ./
RUN uv sync --frozen --no-dev

COPY src/ ./src/
COPY server.py .

# 下载 ONNX 模型（用 python urllib，dev 变体无需 wget/apt）
ARG HF_BASE=https://huggingface.co/palemoky/douzero-baselines/resolve/main/models_onnx/douzero_WP
RUN mkdir -p models && \
    python -c "import urllib.request; base='${HF_BASE}'; [urllib.request.urlretrieve(f'{base}/{p}.onnx', f'models/{p}.onnx') for p in ('landlord','landlord_down','landlord_up')]"

# 运行阶段：distroless python，默认非 root，无 shell / 包管理器
FROM ${PYTHON_RUNTIME_IMAGE}

WORKDIR /app

COPY --from=builder /app/.venv /app/.venv
COPY --from=builder /app/src /app/src
COPY --from=builder /app/server.py /app/server.py
COPY --from=builder /app/models /app/models

ENV DOUZERO_MODELS=/app/models
ENV PORT=2021

EXPOSE 2021

# runtime 镜像自带 python，健康检查保持不变
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://localhost:2021/health')"

CMD ["/app/.venv/bin/python", "server.py"]
