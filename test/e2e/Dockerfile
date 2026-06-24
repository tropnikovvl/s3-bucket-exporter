FROM python:3.13.1-slim

# Bring in the uv binary (pinned) from the official image.
COPY --from=ghcr.io/astral-sh/uv:0.11.23 /uv /uvx /bin/

WORKDIR /app

# Install dependencies first for better layer caching.
COPY pyproject.toml uv.lock ./
RUN uv sync --frozen

COPY tests/ ./tests/

ENV TEST_PROFILE=short

CMD uv run --frozen pytest -v /app/tests/test_${TEST_PROFILE}_running_e2e.py
