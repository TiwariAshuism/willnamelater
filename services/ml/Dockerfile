# Build context is services/ml.
#
#   docker build -f services/ml/Dockerfile services/ml

# ---- build ------------------------------------------------------------------
# python-igraph compiles a C extension. The toolchain is needed to build the
# wheel but not to run it, so it lives here and never reaches the runtime image.
FROM python:3.13-slim AS build

ENV PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_NO_CACHE_DIR=1

RUN apt-get update \
 && apt-get install -y --no-install-recommends build-essential \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# Install into a self-contained venv that the runtime stage copies wholesale.
RUN python -m venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH"

# Copy the manifest alone first so dependency resolution caches independently of
# source edits. setuptools resolves [tool.setuptools] packages at metadata time,
# so EVERY declared package directory must exist here — pyproject declares both
# app and training, and a missing one fails the build outright rather than
# yielding a partial install.
COPY pyproject.toml ./
COPY app/__init__.py ./app/
COPY training/__init__.py ./training/
RUN pip install .

COPY app/ ./app/
COPY training/ ./training/
RUN pip install --no-deps .

# ---- runtime ----------------------------------------------------------------
FROM python:3.13-slim AS runtime

# Run as an unprivileged user. The base image ships only root.
RUN useradd --system --create-home --uid 65532 mlsvc

ENV PATH="/opt/venv/bin:$PATH" \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

COPY --from=build /opt/venv /opt/venv

WORKDIR /app
COPY app/ /app/app/

USER mlsvc

EXPOSE 8000

# curl is absent from slim; python is not, and it is already on PATH.
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=5 \
    CMD ["python", "-c", "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://127.0.0.1:8000/healthz', timeout=2).status==200 else 1)"]

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]
