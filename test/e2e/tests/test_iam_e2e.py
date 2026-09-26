"""IAM authentication e2e check.

A second exporter runs without static keys, with IRSA-style variables
(AWS_ROLE_ARN + AWS_WEB_IDENTITY_TOKEN_FILE) and IMDS disabled. Its only way to
get credentials is the SDK chain calling STS AssumeRoleWithWebIdentity on floci.

floci does not verify request signatures or enforce IAM policies, so this test
proves the credential path (SDK chain -> STS -> temporary keys -> scrape), not
authorization.

Run this module after the profile test: it adds a bucket, and the profile
test compares exporter totals against the whole S3 state.
"""
import logging
import os

import pytest

from conftest import S3_EXPORTER_URL
from e2elib.metrics import fetch_metrics, parse_metrics
from e2elib.wait import wait_until

logger = logging.getLogger(__name__)

S3_EXPORTER_IAM_URL = os.getenv("S3_EXPORTER_IAM_URL", "http://localhost:9656/metrics")

BUCKET = "iam-auth-bucket"
CONTENT = b"scraped with web identity credentials"


@pytest.fixture(scope="module")
def iam_bucket(s3_client):
    s3_client.create_bucket(Bucket=BUCKET)
    s3_client.put_object(Bucket=BUCKET, Key="object.txt", Body=CONTENT)
    logger.info(f"Bucket '{BUCKET}' created with one object")


def test_exporter_scrapes_with_web_identity(iam_bucket):
    def _scraped():
        standard = parse_metrics(fetch_metrics(S3_EXPORTER_IAM_URL))[BUCKET]["storage_classes"]["STANDARD"]
        return standard["object_count"]["current"] == 1 and standard["total_size"]["current"] == len(CONTENT)

    wait_until(_scraped, timeout=60, interval=2)

    metrics = parse_metrics(fetch_metrics(S3_EXPORTER_IAM_URL))
    assert metrics["endpoint_up"] == 1
    attempts = metrics["auth_attempts"]
    assert attempts.get(("iam", "success"), 0) >= 1, attempts
    assert all(status == "success" for _, status in attempts), attempts


def test_key_exporter_reports_keys_method():
    """Control: the default exporter must not report the iam method too."""
    attempts = parse_metrics(fetch_metrics(S3_EXPORTER_URL))["auth_attempts"]
    assert set(attempts) == {("keys", "success")}, attempts
