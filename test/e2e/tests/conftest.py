"""Shared e2e fixtures and configuration."""
import logging
import os

import boto3
import pytest

from e2elib.metrics import fetch_metrics, parse_metrics
from e2elib.wait import wait_until

logging.basicConfig(level=logging.INFO, format="%(asctime)s - %(levelname)s - %(message)s")
logger = logging.getLogger(__name__)

S3_ENDPOINT = os.getenv("S3_ENDPOINT", "http://localhost:4566")
S3_ACCESS_KEY = os.getenv("S3_ACCESS_KEY", "test")
S3_SECRET_KEY = os.getenv("S3_SECRET_KEY", "test")
S3_REGION = os.getenv("S3_REGION", "us-east-1")
S3_EXPORTER_URL = os.getenv("S3_EXPORTER_URL", "http://localhost:9655/metrics")


@pytest.fixture(scope="session")
def s3_client():
    """Create an S3 client from environment configuration."""
    logger.info(f"Creating S3 client with endpoint: {S3_ENDPOINT}")
    return boto3.client(
        "s3",
        endpoint_url=S3_ENDPOINT,
        aws_access_key_id=S3_ACCESS_KEY,
        aws_secret_access_key=S3_SECRET_KEY,
        region_name=S3_REGION,
    )


@pytest.fixture(scope="session")
def exporter_ready():
    """Block until the exporter is serving parseable metrics.

    Readiness only means the exporter is up and has completed a scrape cycle
    (the ``s3_endpoint_up`` series is present). It deliberately does NOT wait
    for ``endpoint_up == 1``: the exporter only sets that once at least one
    bucket exists (see internal/controllers/s3talker.go), which happens later
    in the per-test setup. The ``endpoint_up == 1`` assertion lives in the
    verifier, which runs after buckets are created.
    """
    def _serving():
        metrics = parse_metrics(fetch_metrics(S3_EXPORTER_URL))
        return "endpoint_up" in metrics

    logger.info("Waiting for exporter to start serving metrics...")
    wait_until(_serving, timeout=60, interval=2)
    logger.info("Exporter is serving metrics")
