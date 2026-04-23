FROM python:3.13-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends curl build-essential && rm -rf /var/lib/apt/lists/*
RUN curl -LsSf https://astral.sh/uv/install.sh | sh
ENV PATH="/root/.local/bin:$PATH"
WORKDIR /app
COPY apps/py-worker/pyproject.toml apps/py-worker/README* ./
COPY apps/py-worker/src ./src
RUN uv sync --no-dev
ENV PYTHONPATH=/app/src
EXPOSE 8082
CMD ["uv", "run", "uvicorn", "py_worker.main:app", "--host", "0.0.0.0", "--port", "8082"]
