import pytest
import boto3
import requests
import time
import logging
import os
import random
import string
from typing import Dict, List, Tuple

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s - %(levelname)s - %(message)s",
)
logger = logging.getLogger(__name__)

# Environment configuration
S3_ENDPOINT = os.getenv("S3_ENDPOINT", "http://localhost:4566")
S3_ACCESS_KEY = os.getenv("S3_ACCESS_KEY", "test")
S3_SECRET_KEY = os.getenv("S3_SECRET_KEY", "test")
S3_REGION = os.getenv("S3_REGION", "us-east-1")
S3_EXPORTER_URL = os.getenv("S3_EXPORTER_URL", "http://localhost:9655/metrics")

# Test configuration
TEST_DURATION_SECONDS = 180  # 3 minutes
CHECK_INTERVAL_SECONDS = 10  # Check metrics every 10 seconds
SCRAPE_INTERVAL_SECONDS = 3  # Exporter scrape interval
WARMUP_SECONDS = 10  # Wait time for exporter to collect initial metrics


class TestLongRunningE2E:
    """
    Long-running end-to-end test that simulates a dynamic S3 environment.

    This test runs for 3 minutes and continuously:
    - Creates and deletes files of various sizes
    - Operates across multiple buckets
    - Verifies that the exporter correctly tracks all changes

    Purpose: Ensure the exporter works correctly over time in a changing environment.
    """

    # File size configurations (in bytes)
    SMALL_FILE_SIZE = 1024  # 1 KB
    MEDIUM_FILE_SIZE = 1024 * 100  # 100 KB
    LARGE_FILE_SIZE = 1024 * 1024  # 1 MB
    XLARGE_FILE_SIZE = 1024 * 1024 * 5  # 5 MB

    @pytest.fixture(scope="class")
    def s3_client(self):
        """Create S3 client using environment variables."""
        logger.info(f"Creating S3 client with endpoint: {S3_ENDPOINT}")
        return boto3.client(
            "s3",
            endpoint_url=S3_ENDPOINT,
            aws_access_key_id=S3_ACCESS_KEY,
            aws_secret_access_key=S3_SECRET_KEY,
            region_name=S3_REGION,
        )

    @pytest.fixture(scope="class")
    def test_buckets(self, s3_client):
        """Create test buckets for the long-running test."""
        time.sleep(5)  # Wait for services to be ready

        buckets = [
            "long-test-bucket-1",
            "long-test-bucket-2",
            "long-test-bucket-3",
            "long-test-bucket-4",
        ]

        logger.info(f"Creating {len(buckets)} test buckets...")
        for bucket in buckets:
            try:
                s3_client.create_bucket(Bucket=bucket)
                logger.info(f"✓ Bucket '{bucket}' created")
            except s3_client.exceptions.BucketAlreadyExists:
                logger.info(f"Bucket '{bucket}' already exists")
            except Exception as e:
                logger.error(f"Failed to create bucket '{bucket}': {e}")
                raise

        yield buckets

    def generate_random_content(self, size: int) -> bytes:
        """Generate random content of specified size."""
        return ''.join(random.choices(string.ascii_letters + string.digits, k=size)).encode()

    def generate_random_key(self) -> str:
        """Generate a random object key."""
        timestamp = int(time.time() * 1000)
        random_str = ''.join(random.choices(string.ascii_lowercase, k=8))
        return f"test-file-{timestamp}-{random_str}.dat"

    def upload_random_file(self, s3_client, bucket: str, size: int) -> Tuple[str, int]:
        """
        Upload a random file to the specified bucket.

        Returns:
            Tuple of (key, actual_size)
        """
        key = self.generate_random_key()
        content = self.generate_random_content(size)
        actual_size = len(content)

        s3_client.put_object(Bucket=bucket, Key=key, Body=content)
        logger.info(f"→ Uploaded {key} to {bucket} ({actual_size} bytes)")

        return key, actual_size

    def delete_random_file(self, s3_client, bucket: str, files: List[str]) -> str:
        """
        Delete a random file from the specified bucket.

        Returns:
            Key of deleted file or None if no files to delete
        """
        if not files:
            return None

        key = random.choice(files)
        s3_client.delete_object(Bucket=bucket, Key=key)
        logger.info(f"← Deleted {key} from {bucket}")

        return key

    def get_actual_bucket_state(self, s3_client, buckets: List[str]) -> Dict:
        """
        Get the actual state of all buckets from S3.

        Returns:
            Dictionary with bucket metadata including object count and total size
        """
        state = {}

        for bucket in buckets:
            try:
                paginator = s3_client.get_paginator('list_objects_v2')
                total_size = 0
                object_count = 0

                for page in paginator.paginate(Bucket=bucket):
                    if 'Contents' in page:
                        for obj in page['Contents']:
                            total_size += obj['Size']
                            object_count += 1

                state[bucket] = {
                    'object_count': object_count,
                    'total_size': total_size
                }
            except Exception as e:
                logger.error(f"Failed to get state for bucket '{bucket}': {e}")
                state[bucket] = {'object_count': 0, 'total_size': 0}

        return state

    def fetch_exporter_metrics(self) -> str:
        """Fetch metrics from the S3 exporter."""
        try:
            response = requests.get(S3_EXPORTER_URL, timeout=5)
            response.raise_for_status()
            return response.text
        except requests.RequestException as e:
            logger.error(f"Failed to fetch metrics from exporter: {e}")
            raise

    def parse_exporter_metrics(self, metrics_text: str) -> Dict:
        """Parse exporter metrics into a structured dictionary."""
        parsed_metrics = {}

        for line in metrics_text.splitlines():
            if line.startswith("#") or not line.strip():
                continue

            try:
                if 's3_bucket_object_number' in line:
                    bucket = line.split('bucketName="')[1].split('"')[0]
                    storage_class = line.split('storageClass="')[1].split('"')[0]
                    count = float(line.split()[-1])
                    parsed_metrics.setdefault(bucket, {}).setdefault("storage_classes", {}).setdefault(storage_class, {})["object_count"] = count

                elif 's3_bucket_size' in line:
                    bucket = line.split('bucketName="')[1].split('"')[0]
                    storage_class = line.split('storageClass="')[1].split('"')[0]
                    size = float(line.split()[-1])
                    parsed_metrics.setdefault(bucket, {}).setdefault("storage_classes", {}).setdefault(storage_class, {})["total_size"] = size

                elif 's3_endpoint_up' in line:
                    endpoint_up = float(line.split()[-1])
                    parsed_metrics["endpoint_up"] = endpoint_up

                elif 's3_bucket_count' in line:
                    bucket_count = float(line.split()[-1])
                    parsed_metrics["bucket_count"] = bucket_count

            except (IndexError, ValueError) as e:
                logger.debug(f"Skipping metrics line: {line}. Error: {e}")

        return parsed_metrics

    def verify_metrics_match_state(self, actual_state: Dict, exporter_metrics: Dict, check_number: int):
        """
        Verify that exporter metrics match the actual S3 state.

        Args:
            actual_state: Dictionary with actual bucket states from S3
            exporter_metrics: Dictionary with parsed exporter metrics
            check_number: The check iteration number for logging
        """
        logger.info(f"--- Check #{check_number}: Verifying metrics ---")

        # Verify endpoint is up
        assert exporter_metrics.get("endpoint_up") == 1, \
            f"Check #{check_number}: Endpoint should be up"

        # Verify bucket count (exporter counts all buckets, including empty ones)
        expected_bucket_count = len(actual_state)
        actual_bucket_count = exporter_metrics.get("bucket_count", 0)
        assert actual_bucket_count == expected_bucket_count, \
            f"Check #{check_number}: Bucket count mismatch. Expected: {expected_bucket_count}, Got: {actual_bucket_count}"

        # Verify each bucket's metrics
        storage_class = "STANDARD"  # Assuming STANDARD storage class
        total_errors = []

        for bucket, state in actual_state.items():
            expected_count = state['object_count']
            expected_size = state['total_size']

            # Empty buckets may not appear in exporter metrics
            if bucket not in exporter_metrics:
                if expected_count > 0 or expected_size > 0:
                    total_errors.append(
                        f"Bucket '{bucket}' missing from exporter metrics (expected {expected_count} objects, {expected_size} bytes)"
                    )
                else:
                    logger.info(
                        f"  ✓ {bucket}: empty bucket (not in metrics, which is expected)"
                    )
                continue

            bucket_metrics = exporter_metrics[bucket].get("storage_classes", {}).get(storage_class, {})

            # Verify object count
            actual_count = bucket_metrics.get('object_count', 0)

            if actual_count != expected_count:
                total_errors.append(
                    f"Bucket '{bucket}' object count mismatch. Expected: {expected_count}, Got: {actual_count}"
                )

            # Verify size (allow small tolerance for rounding)
            actual_size = bucket_metrics.get('total_size', 0)

            if abs(actual_size - expected_size) > 1:
                total_errors.append(
                    f"Bucket '{bucket}' size mismatch. Expected: {expected_size}, Got: {actual_size}"
                )

            logger.info(
                f"  ✓ {bucket}: objects={expected_count}/{actual_count}, size={expected_size}/{actual_size}"
            )

        if total_errors:
            error_msg = f"Check #{check_number} failed with {len(total_errors)} error(s):\n" + "\n".join(total_errors)
            logger.error(error_msg)
            raise AssertionError(error_msg)

        logger.info(f"✓ Check #{check_number}: All metrics verified successfully")

    def test_long_running_dynamic_s3_operations(self, s3_client, test_buckets):
        """
        Long-running test that performs dynamic S3 operations and verifies exporter correctness.

        This test runs for 3 minutes and continuously:
        1. Adds files of various sizes to different buckets
        2. Deletes random files from buckets
        3. Verifies that the exporter correctly tracks all changes

        The test ensures the exporter works correctly over time in a changing environment.
        """
        logger.info("=" * 80)
        logger.info("Starting Long-Running E2E Test")
        logger.info(f"Duration: {TEST_DURATION_SECONDS} seconds ({TEST_DURATION_SECONDS // 60} minutes)")
        logger.info(f"Check interval: {CHECK_INTERVAL_SECONDS} seconds")
        logger.info(f"Buckets: {test_buckets}")
        logger.info("=" * 80)

        # Track files in each bucket
        bucket_files: Dict[str, List[str]] = {bucket: [] for bucket in test_buckets}

        # Wait for exporter to initialize
        logger.info(f"Waiting {WARMUP_SECONDS} seconds for exporter to initialize...")
        time.sleep(WARMUP_SECONDS)

        start_time = time.time()
        check_number = 0
        operation_count = 0

        while True:
            elapsed_time = time.time() - start_time

            # Check if test duration has been reached
            if elapsed_time >= TEST_DURATION_SECONDS:
                logger.info(f"Test duration reached ({TEST_DURATION_SECONDS} seconds). Stopping...")
                break

            remaining_time = TEST_DURATION_SECONDS - elapsed_time
            logger.info(f"\n--- Time: {elapsed_time:.1f}s / {TEST_DURATION_SECONDS}s (remaining: {remaining_time:.1f}s) ---")

            # Perform random S3 operations
            num_operations = random.randint(2, 5)
            logger.info(f"Performing {num_operations} random operations...")

            for _ in range(num_operations):
                bucket = random.choice(test_buckets)
                operation = random.choice(['add', 'delete'])

                if operation == 'add':
                    # Choose random file size
                    size = random.choice([
                        self.SMALL_FILE_SIZE,
                        self.MEDIUM_FILE_SIZE,
                        self.LARGE_FILE_SIZE,
                        self.XLARGE_FILE_SIZE,
                    ])

                    key, _ = self.upload_random_file(s3_client, bucket, size)
                    bucket_files[bucket].append(key)
                    operation_count += 1

                elif operation == 'delete' and bucket_files[bucket]:
                    key = self.delete_random_file(s3_client, bucket, bucket_files[bucket])
                    if key:
                        bucket_files[bucket].remove(key)
                        operation_count += 1

            # Wait for exporter to scrape metrics
            # Use 2x scrape interval to ensure at least one scrape has completed
            wait_time = SCRAPE_INTERVAL_SECONDS * 2
            logger.info(f"Waiting {wait_time} seconds for exporter to scrape metrics...")
            time.sleep(wait_time)

            # Get actual state from S3
            actual_state = self.get_actual_bucket_state(s3_client, test_buckets)

            # Get metrics from exporter
            metrics_text = self.fetch_exporter_metrics()
            exporter_metrics = self.parse_exporter_metrics(metrics_text)

            # Verify metrics match actual state
            check_number += 1
            self.verify_metrics_match_state(actual_state, exporter_metrics, check_number)

            # Calculate time until next check
            next_check_time = CHECK_INTERVAL_SECONDS - wait_time
            if next_check_time > 0:
                logger.info(f"Waiting {next_check_time} seconds until next check...")
                time.sleep(next_check_time)

        # Final verification
        logger.info("\n" + "=" * 80)
        logger.info("Performing final verification...")
        logger.info("=" * 80)

        # Wait for final scrape
        time.sleep(SCRAPE_INTERVAL_SECONDS * 2)

        actual_state = self.get_actual_bucket_state(s3_client, test_buckets)
        metrics_text = self.fetch_exporter_metrics()
        exporter_metrics = self.parse_exporter_metrics(metrics_text)

        check_number += 1
        self.verify_metrics_match_state(actual_state, exporter_metrics, check_number)

        # Print test summary
        logger.info("\n" + "=" * 80)
        logger.info("LONG-RUNNING E2E TEST COMPLETED SUCCESSFULLY")
        logger.info(f"Total duration: {time.time() - start_time:.1f} seconds")
        logger.info(f"Total operations performed: {operation_count}")
        logger.info(f"Total verification checks: {check_number}")
        logger.info(f"Buckets tested: {len(test_buckets)}")
        logger.info("=" * 80)


if __name__ == "__main__":
    import sys
    exit_code = pytest.main([__file__, "-v", "-s"])
    sys.exit(exit_code)
