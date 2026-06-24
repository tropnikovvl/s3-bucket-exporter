import logging
import random
import string
import time
from typing import Dict, Set, Tuple

import pytest

from e2elib.metrics import fetch_metrics, parse_metrics
from e2elib.state import get_actual_bucket_state
from e2elib.verify import verify_with_retry
from conftest import S3_EXPORTER_URL

logger = logging.getLogger(__name__)

# Test configuration
TEST_DURATION_SECONDS = 180  # 3 minutes
SCRAPE_INTERVAL_SECONDS = 3  # Exporter scrape interval (poll cadence for verification)


class TestLongRunningE2E:
    """
    Long-running end-to-end test that simulates a dynamic S3 environment.

    This test runs for 3 minutes and continuously:
    - Creates and deletes files of various sizes
    - Operates across multiple buckets (including versioned ones)
    - Verifies that the exporter correctly tracks all changes including
      current versions, noncurrent versions, and delete markers.

    Purpose: Ensure the exporter works correctly over time in a changing environment.
    """

    # File size configurations (in bytes)
    SMALL_FILE_SIZE = 1024  # 1 KB
    MEDIUM_FILE_SIZE = 1024 * 100  # 100 KB
    LARGE_FILE_SIZE = 1024 * 1024  # 1 MB
    XLARGE_FILE_SIZE = 1024 * 1024 * 5  # 5 MB

    # Storage classes exercised by uploads/overwrites; the exporter must track
    # each independently (verification derives truth from S3, no bookkeeping here).
    STORAGE_CLASSES = ["STANDARD", "GLACIER", "STANDARD_IA"]

    @pytest.fixture(scope="class")
    def test_buckets(self, s3_client):
        """Create test buckets for the long-running test, including versioned ones."""
        plain_buckets = [
            "long-test-bucket-1",
            "long-test-bucket-2",
        ]
        versioned_buckets = [
            "long-test-versioned-1",
            "long-test-versioned-2",
        ]
        all_buckets = plain_buckets + versioned_buckets

        logger.info(f"Creating {len(all_buckets)} test buckets...")
        for bucket in all_buckets:
            try:
                s3_client.create_bucket(Bucket=bucket)
                logger.info(f"Bucket '{bucket}' created")
            except s3_client.exceptions.BucketAlreadyExists:
                logger.info(f"Bucket '{bucket}' already exists")
            except Exception as e:
                logger.error(f"Failed to create bucket '{bucket}': {e}")
                raise

        # Enable versioning on versioned buckets
        for bucket in versioned_buckets:
            s3_client.put_bucket_versioning(
                Bucket=bucket,
                VersioningConfiguration={"Status": "Enabled"},
            )
            logger.info(f"Versioning enabled on '{bucket}'")

        yield {
            "all": all_buckets,
            "plain": plain_buckets,
            "versioned": versioned_buckets,
        }

    def generate_random_content(self, size: int) -> bytes:
        """Generate random content of specified size (used only for its length)."""
        return random.randbytes(size)

    def generate_random_key(self) -> str:
        """Generate a random object key."""
        timestamp = int(time.time() * 1000)
        random_str = ''.join(random.choices(string.ascii_lowercase, k=8))
        return f"test-file-{timestamp}-{random_str}.dat"

    def upload_random_file(self, s3_client, bucket: str, size: int) -> Tuple[str, int]:
        """Upload a random file to the specified bucket. Returns (key, actual_size)."""
        key = self.generate_random_key()
        content = self.generate_random_content(size)
        actual_size = len(content)
        storage_class = random.choice(self.STORAGE_CLASSES)

        s3_client.put_object(Bucket=bucket, Key=key, Body=content, StorageClass=storage_class)
        logger.info(f"  -> Uploaded {key} to {bucket} ({actual_size} bytes, {storage_class})")

        return key, actual_size

    def overwrite_file(self, s3_client, bucket: str, key: str, size: int) -> int:
        """Overwrite an existing file (creates noncurrent version on versioned buckets). Returns new size."""
        content = self.generate_random_content(size)
        actual_size = len(content)
        storage_class = random.choice(self.STORAGE_CLASSES)

        s3_client.put_object(Bucket=bucket, Key=key, Body=content, StorageClass=storage_class)
        logger.info(f"  -> Overwrote {key} in {bucket} ({actual_size} bytes, {storage_class})")

        return actual_size

    def delete_file(self, s3_client, bucket: str, key: str):
        """Delete a file from the specified bucket."""
        s3_client.delete_object(Bucket=bucket, Key=key)
        logger.info(f"  <- Deleted {key} from {bucket}")

    def test_long_running_dynamic_s3_operations(self, s3_client, test_buckets, exporter_ready):
        """
        Long-running test that performs dynamic S3 operations and verifies exporter correctness.

        This test runs for 3 minutes and continuously:
        1. Adds files of various sizes to different buckets
        2. Overwrites files in versioned buckets (creating noncurrent versions)
        3. Deletes files (creating delete markers in versioned buckets)
        4. Verifies that the exporter correctly tracks all changes
        """
        logger.info("=" * 80)
        logger.info("Starting Long-Running E2E Test")
        logger.info(f"Duration: {TEST_DURATION_SECONDS} seconds ({TEST_DURATION_SECONDS // 60} minutes)")
        logger.info(f"Plain buckets: {test_buckets['plain']}")
        logger.info(f"Versioned buckets: {test_buckets['versioned']}")
        logger.info("=" * 80)

        # Track which object keys currently exist per bucket (to pick
        # delete/overwrite targets). Sizes/classes are not tracked here —
        # verification derives all truth from S3 via get_actual_bucket_state.
        bucket_files: Dict[str, Set[str]] = {bucket: set() for bucket in test_buckets["all"]}

        start_time = time.time()
        check_number = 0
        operation_count = 0

        while True:
            elapsed_time = time.time() - start_time

            if elapsed_time >= TEST_DURATION_SECONDS:
                logger.info(f"Test duration reached ({TEST_DURATION_SECONDS} seconds). Stopping...")
                break

            remaining_time = TEST_DURATION_SECONDS - elapsed_time
            logger.info(f"\n--- Time: {elapsed_time:.1f}s / {TEST_DURATION_SECONDS}s (remaining: {remaining_time:.1f}s) ---")

            # Perform random S3 operations
            num_operations = random.randint(2, 5)
            logger.info(f"Performing {num_operations} random operations...")

            for _ in range(num_operations):
                bucket = random.choice(test_buckets["all"])
                is_versioned = bucket in test_buckets["versioned"]

                # Choose operation: add, delete, or overwrite (versioned only)
                if is_versioned and bucket_files[bucket]:
                    operation = random.choice(['add', 'delete', 'overwrite'])
                elif bucket_files[bucket]:
                    operation = random.choice(['add', 'delete'])
                else:
                    operation = 'add'

                if operation == 'add':
                    size = random.choice([
                        self.SMALL_FILE_SIZE,
                        self.MEDIUM_FILE_SIZE,
                        self.LARGE_FILE_SIZE,
                        self.XLARGE_FILE_SIZE,
                    ])
                    key, _ = self.upload_random_file(s3_client, bucket, size)
                    bucket_files[bucket].add(key)
                    operation_count += 1

                elif operation == 'delete':
                    key = random.choice(list(bucket_files[bucket]))
                    self.delete_file(s3_client, bucket, key)
                    bucket_files[bucket].discard(key)
                    operation_count += 1

                elif operation == 'overwrite':
                    key = random.choice(list(bucket_files[bucket]))
                    new_size = random.choice([
                        self.SMALL_FILE_SIZE,
                        self.MEDIUM_FILE_SIZE,
                    ])
                    self.overwrite_file(s3_client, bucket, key, new_size)
                    operation_count += 1

            # Snapshot S3, then poll until the exporter converges to it. This
            # replaces a fixed sleep: it finishes as soon as the next scrape
            # lands and tolerates a slow scrape instead of racing it.
            check_number += 1
            actual_state = get_actual_bucket_state(s3_client, test_buckets["all"])
            verify_with_retry(
                actual_state,
                lambda: parse_metrics(fetch_metrics(S3_EXPORTER_URL)),
                label=f"Check #{check_number}",
                interval=SCRAPE_INTERVAL_SECONDS,
            )

        # Final verification
        logger.info("\n" + "=" * 80)
        logger.info("Performing final verification...")
        logger.info("=" * 80)

        check_number += 1
        actual_state = get_actual_bucket_state(s3_client, test_buckets["all"])
        verify_with_retry(
            actual_state,
            lambda: parse_metrics(fetch_metrics(S3_EXPORTER_URL)),
            label=f"Check #{check_number} (final)",
            interval=SCRAPE_INTERVAL_SECONDS,
        )

        # Print test summary
        logger.info("\n" + "=" * 80)
        logger.info("LONG-RUNNING E2E TEST COMPLETED SUCCESSFULLY")
        logger.info(f"Total duration: {time.time() - start_time:.1f} seconds")
        logger.info(f"Total operations performed: {operation_count}")
        logger.info(f"Total verification checks: {check_number}")
        logger.info(f"Buckets tested: {len(test_buckets['all'])} ({len(test_buckets['plain'])} plain, {len(test_buckets['versioned'])} versioned)")
        logger.info("=" * 80)


if __name__ == "__main__":
    import sys
    exit_code = pytest.main([__file__, "-v", "-s"])
    sys.exit(exit_code)
