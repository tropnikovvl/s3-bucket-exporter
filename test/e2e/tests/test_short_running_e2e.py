import logging
import time

import pytest

from e2elib.metrics import fetch_metrics, parse_metrics
from e2elib.state import get_actual_bucket_state
from e2elib.verify import verify_metrics_match_state
from conftest import S3_EXPORTER_URL

logger = logging.getLogger(__name__)

PLAIN_BUCKETS = ["test-bucket-1", "test-bucket-2"]
VERSIONED_BUCKET = "test-bucket-versioned"
ALL_BUCKETS = PLAIN_BUCKETS + [VERSIONED_BUCKET]

FILES = [
    {"bucket": "test-bucket-1", "key": "file1.txt", "content": "Hello World" * 100},
    {"bucket": "test-bucket-1", "key": "file2.txt", "content": "Test Content" * 50},
    {"bucket": "test-bucket-2", "key": "data.txt", "content": "Random Data" * 75},
]

# Independent oracle: the fixed, deterministic scenario below must produce
# exactly this state in S3. Asserting it before comparing the exporter restores
# the literal-expectation oracle the suite had before the refactor — it guards
# against test-setup drift and a buggy list_object_versions, neither of which a
# state-derived comparison alone would catch (since the exporter reads the same
# listing). Sizes are byte lengths of the contents created in populate_s3.
EXPECTED_STATE = {
    # file1.txt ("Hello World"*100=1100) + file2.txt ("Test Content"*50=600)
    "test-bucket-1": {
        "current_count": 2, "current_size": 1700,
        "noncurrent_count": 0, "noncurrent_size": 0, "delete_markers": 0,
    },
    # data.txt ("Random Data"*75=825)
    "test-bucket-2": {
        "current_count": 1, "current_size": 825,
        "noncurrent_count": 0, "noncurrent_size": 0, "delete_markers": 0,
    },
    # current: versioned.txt v2 (27) + regular.txt (24); noncurrent: versioned.txt
    # v1 (19) + to-delete.txt original (20); 1 delete marker for to-delete.txt
    "test-bucket-versioned": {
        "current_count": 2, "current_size": 51,
        "noncurrent_count": 2, "noncurrent_size": 39, "delete_markers": 1,
    },
}


class TestS3BucketExporter:
    @pytest.fixture(scope="class")
    def populate_s3(self, s3_client):
        """Create plain + versioned buckets and the fixed object scenario."""
        for bucket in PLAIN_BUCKETS:
            s3_client.create_bucket(Bucket=bucket)
            logger.info(f"Bucket '{bucket}' created")
        for f in FILES:
            s3_client.put_object(Bucket=f["bucket"], Key=f["key"], Body=f["content"].encode())
            logger.info(f"Uploaded '{f['key']}' to '{f['bucket']}'")

        s3_client.create_bucket(Bucket=VERSIONED_BUCKET)
        s3_client.put_bucket_versioning(
            Bucket=VERSIONED_BUCKET,
            VersioningConfiguration={"Status": "Enabled"},
        )
        logger.info(f"Versioned bucket '{VERSIONED_BUCKET}' created")
        # versioned.txt: v1 becomes noncurrent after v2
        s3_client.put_object(Bucket=VERSIONED_BUCKET, Key="versioned.txt", Body=b"version one content")
        s3_client.put_object(Bucket=VERSIONED_BUCKET, Key="versioned.txt", Body=b"version two content updated")
        # to-delete.txt: original becomes noncurrent + delete marker
        s3_client.put_object(Bucket=VERSIONED_BUCKET, Key="to-delete.txt", Body=b"this will be deleted")
        s3_client.delete_object(Bucket=VERSIONED_BUCKET, Key="to-delete.txt")
        # regular.txt: single current version
        s3_client.put_object(Bucket=VERSIONED_BUCKET, Key="regular.txt", Body=b"regular file no versions")
        logger.info("Versioned scenario populated")

    def test_exporter_metrics(self, s3_client, populate_s3, exporter_ready):
        """Verify exporter metrics match the actual S3 state."""
        time.sleep(10)  # allow at least one scrape cycle after population
        actual_state = get_actual_bucket_state(s3_client, ALL_BUCKETS)
        assert actual_state == EXPECTED_STATE, (
            f"S3 state does not match the intended scenario.\n"
            f"Expected: {EXPECTED_STATE}\nGot: {actual_state}"
        )
        metrics = parse_metrics(fetch_metrics(S3_EXPORTER_URL))
        verify_metrics_match_state(actual_state, metrics, label="short-running")
        logger.info("All short-running checks passed")


if __name__ == "__main__":
    import sys
    sys.exit(pytest.main([__file__, "-v", "-s"]))
