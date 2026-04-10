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
        """Create test buckets for the long-running test, including versioned ones."""
        time.sleep(5)  # Wait for services to be ready

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
        """Generate random content of specified size."""
        return ''.join(random.choices(string.ascii_letters + string.digits, k=size)).encode()

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

        s3_client.put_object(Bucket=bucket, Key=key, Body=content)
        logger.info(f"  -> Uploaded {key} to {bucket} ({actual_size} bytes)")

        return key, actual_size

    def overwrite_file(self, s3_client, bucket: str, key: str, size: int) -> int:
        """Overwrite an existing file (creates noncurrent version on versioned buckets). Returns new size."""
        content = self.generate_random_content(size)
        actual_size = len(content)

        s3_client.put_object(Bucket=bucket, Key=key, Body=content)
        logger.info(f"  -> Overwrote {key} in {bucket} ({actual_size} bytes)")

        return actual_size

    def delete_file(self, s3_client, bucket: str, key: str):
        """Delete a file from the specified bucket."""
        s3_client.delete_object(Bucket=bucket, Key=key)
        logger.info(f"  <- Deleted {key} from {bucket}")

    def get_actual_bucket_state(self, s3_client, bucket_info: Dict) -> Dict:
        """
        Get the actual state of all buckets from S3 using list_object_versions.

        Returns a dictionary with current/noncurrent counts and sizes, plus delete markers.
        """
        state = {}

        for bucket in bucket_info["all"]:
            try:
                current_count = 0
                current_size = 0
                noncurrent_count = 0
                noncurrent_size = 0
                delete_markers = 0

                paginator = s3_client.get_paginator('list_object_versions')
                for page in paginator.paginate(Bucket=bucket):
                    for ver in page.get('Versions', []):
                        if ver.get('IsLatest', False):
                            current_count += 1
                            current_size += ver['Size']
                        else:
                            noncurrent_count += 1
                            noncurrent_size += ver['Size']

                    for _ in page.get('DeleteMarkers', []):
                        delete_markers += 1

                state[bucket] = {
                    'current_count': current_count,
                    'current_size': current_size,
                    'noncurrent_count': noncurrent_count,
                    'noncurrent_size': noncurrent_size,
                    'delete_markers': delete_markers,
                }
            except Exception as e:
                logger.error(f"Failed to get state for bucket '{bucket}': {e}")
                state[bucket] = {
                    'current_count': 0, 'current_size': 0,
                    'noncurrent_count': 0, 'noncurrent_size': 0,
                    'delete_markers': 0,
                }

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
        """Parse exporter metrics into a structured dictionary with version status."""
        parsed_metrics = {}

        for line in metrics_text.splitlines():
            if line.startswith("#") or not line.strip():
                continue

            try:
                if 's3_bucket_delete_markers' in line:
                    bucket = line.split('bucketName="')[1].split('"')[0]
                    count = float(line.split()[-1])
                    parsed_metrics.setdefault(bucket, {})["delete_markers"] = count

                elif 's3_total_delete_markers' in line:
                    count = float(line.split()[-1])
                    parsed_metrics["total_delete_markers"] = count

                elif 's3_bucket_object_number' in line:
                    bucket = line.split('bucketName="')[1].split('"')[0]
                    storage_class = line.split('storageClass="')[1].split('"')[0]
                    version_status = line.split('versionStatus="')[1].split('"')[0]
                    count = float(line.split()[-1])
                    parsed_metrics.setdefault(bucket, {}).setdefault("storage_classes", {}).setdefault(storage_class, {}).setdefault("object_count", {})[version_status] = count

                elif 's3_bucket_size' in line:
                    bucket = line.split('bucketName="')[1].split('"')[0]
                    storage_class = line.split('storageClass="')[1].split('"')[0]
                    version_status = line.split('versionStatus="')[1].split('"')[0]
                    size = float(line.split()[-1])
                    parsed_metrics.setdefault(bucket, {}).setdefault("storage_classes", {}).setdefault(storage_class, {}).setdefault("total_size", {})[version_status] = size

                elif 's3_total_object_number' in line:
                    storage_class = line.split('storageClass="')[1].split('"')[0]
                    version_status = line.split('versionStatus="')[1].split('"')[0]
                    total_objects = float(line.split()[-1])
                    parsed_metrics.setdefault("total", {}).setdefault("storage_classes", {}).setdefault(storage_class, {}).setdefault("object_count", {})[version_status] = total_objects

                elif 's3_total_size' in line:
                    storage_class = line.split('storageClass="')[1].split('"')[0]
                    version_status = line.split('versionStatus="')[1].split('"')[0]
                    total_size = float(line.split()[-1])
                    parsed_metrics.setdefault("total", {}).setdefault("storage_classes", {}).setdefault(storage_class, {}).setdefault("total_size", {})[version_status] = total_size

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
        """Verify that exporter metrics match the actual S3 state."""
        logger.info(f"--- Check #{check_number}: Verifying metrics ---")

        # Verify endpoint is up
        assert exporter_metrics.get("endpoint_up") == 1, \
            f"Check #{check_number}: Endpoint should be up"

        # Verify bucket count (exporter counts all buckets, including empty ones)
        expected_bucket_count = len(actual_state)
        actual_bucket_count = exporter_metrics.get("bucket_count", 0)
        assert actual_bucket_count == expected_bucket_count, \
            f"Check #{check_number}: Bucket count mismatch. Expected: {expected_bucket_count}, Got: {actual_bucket_count}"

        storage_class = "STANDARD"
        total_errors = []

        # Verify per-bucket metrics
        for bucket, state in actual_state.items():
            bucket_data = exporter_metrics.get(bucket, {})
            bucket_metrics = bucket_data.get("storage_classes", {}).get(storage_class, {})

            # Verify current object count
            actual_current_count = bucket_metrics.get("object_count", {}).get("current", 0)
            if actual_current_count != state['current_count']:
                total_errors.append(
                    f"Bucket '{bucket}' current object count mismatch. "
                    f"Expected: {state['current_count']}, Got: {actual_current_count}"
                )

            # Verify current size
            actual_current_size = bucket_metrics.get("total_size", {}).get("current", 0)
            if actual_current_size != state['current_size']:
                total_errors.append(
                    f"Bucket '{bucket}' current size mismatch. "
                    f"Expected: {state['current_size']}, Got: {actual_current_size}"
                )

            # Verify noncurrent object count
            actual_noncurrent_count = bucket_metrics.get("object_count", {}).get("noncurrent", 0)
            if actual_noncurrent_count != state['noncurrent_count']:
                total_errors.append(
                    f"Bucket '{bucket}' noncurrent object count mismatch. "
                    f"Expected: {state['noncurrent_count']}, Got: {actual_noncurrent_count}"
                )

            # Verify noncurrent size
            actual_noncurrent_size = bucket_metrics.get("total_size", {}).get("noncurrent", 0)
            if actual_noncurrent_size != state['noncurrent_size']:
                total_errors.append(
                    f"Bucket '{bucket}' noncurrent size mismatch. "
                    f"Expected: {state['noncurrent_size']}, Got: {actual_noncurrent_size}"
                )

            # Verify delete markers
            actual_dm = bucket_data.get("delete_markers", 0)
            if actual_dm != state['delete_markers']:
                total_errors.append(
                    f"Bucket '{bucket}' delete markers mismatch. "
                    f"Expected: {state['delete_markers']}, Got: {actual_dm}"
                )

            logger.info(
                f"  OK {bucket}: current={state['current_count']}/{int(actual_current_count)}, "
                f"noncurrent={state['noncurrent_count']}/{int(actual_noncurrent_count)}, "
                f"dm={state['delete_markers']}/{int(actual_dm)}"
            )

        if total_errors:
            error_msg = f"Check #{check_number} failed with {len(total_errors)} error(s):\n" + "\n".join(total_errors)
            logger.error(error_msg)
            raise AssertionError(error_msg)

        # Verify total metrics
        logger.info("  Verifying total metrics...")
        expected_total_current_count = sum(s['current_count'] for s in actual_state.values())
        expected_total_current_size = sum(s['current_size'] for s in actual_state.values())
        expected_total_noncurrent_count = sum(s['noncurrent_count'] for s in actual_state.values())
        expected_total_noncurrent_size = sum(s['noncurrent_size'] for s in actual_state.values())
        expected_total_dm = sum(s['delete_markers'] for s in actual_state.values())

        total_metrics = exporter_metrics.get("total", {}).get("storage_classes", {}).get(storage_class, {})

        actual_total_current_count = total_metrics.get("object_count", {}).get("current", 0)
        actual_total_noncurrent_count = total_metrics.get("object_count", {}).get("noncurrent", 0)
        actual_total_current_size = total_metrics.get("total_size", {}).get("current", 0)
        actual_total_noncurrent_size = total_metrics.get("total_size", {}).get("noncurrent", 0)
        actual_total_dm = exporter_metrics.get("total_delete_markers", 0)

        if actual_total_current_count != expected_total_current_count:
            raise AssertionError(
                f"Check #{check_number}: Total current object count mismatch. "
                f"Expected: {expected_total_current_count}, Got: {actual_total_current_count}"
            )
        if actual_total_current_size != expected_total_current_size:
            raise AssertionError(
                f"Check #{check_number}: Total current size mismatch. "
                f"Expected: {expected_total_current_size}, Got: {actual_total_current_size}"
            )
        if actual_total_noncurrent_count != expected_total_noncurrent_count:
            raise AssertionError(
                f"Check #{check_number}: Total noncurrent object count mismatch. "
                f"Expected: {expected_total_noncurrent_count}, Got: {actual_total_noncurrent_count}"
            )
        if actual_total_noncurrent_size != expected_total_noncurrent_size:
            raise AssertionError(
                f"Check #{check_number}: Total noncurrent size mismatch. "
                f"Expected: {expected_total_noncurrent_size}, Got: {actual_total_noncurrent_size}"
            )
        if actual_total_dm != expected_total_dm:
            raise AssertionError(
                f"Check #{check_number}: Total delete markers mismatch. "
                f"Expected: {expected_total_dm}, Got: {actual_total_dm}"
            )

        logger.info(
            f"  OK Total: current_obj={expected_total_current_count}/{int(actual_total_current_count)}, "
            f"noncurrent_obj={expected_total_noncurrent_count}/{int(actual_total_noncurrent_count)}, "
            f"dm={expected_total_dm}/{int(actual_total_dm)}"
        )
        logger.info(f"Check #{check_number}: All metrics verified successfully")

    def test_long_running_dynamic_s3_operations(self, s3_client, test_buckets):
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
        logger.info(f"Check interval: {CHECK_INTERVAL_SECONDS} seconds")
        logger.info(f"Plain buckets: {test_buckets['plain']}")
        logger.info(f"Versioned buckets: {test_buckets['versioned']}")
        logger.info("=" * 80)

        # Track files in each bucket: key -> size
        bucket_files: Dict[str, Dict[str, int]] = {bucket: {} for bucket in test_buckets["all"]}

        # Wait for exporter to initialize
        logger.info(f"Waiting {WARMUP_SECONDS} seconds for exporter to initialize...")
        time.sleep(WARMUP_SECONDS)

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
                    key, actual_size = self.upload_random_file(s3_client, bucket, size)
                    bucket_files[bucket][key] = actual_size
                    operation_count += 1

                elif operation == 'delete':
                    key = random.choice(list(bucket_files[bucket].keys()))
                    self.delete_file(s3_client, bucket, key)
                    del bucket_files[bucket][key]
                    operation_count += 1

                elif operation == 'overwrite':
                    key = random.choice(list(bucket_files[bucket].keys()))
                    new_size = random.choice([
                        self.SMALL_FILE_SIZE,
                        self.MEDIUM_FILE_SIZE,
                    ])
                    actual_size = self.overwrite_file(s3_client, bucket, key, new_size)
                    bucket_files[bucket][key] = actual_size
                    operation_count += 1

            # Wait for exporter to scrape metrics
            wait_time = SCRAPE_INTERVAL_SECONDS * 2
            logger.info(f"Waiting {wait_time} seconds for exporter to scrape metrics...")
            time.sleep(wait_time)

            # Get actual state from S3 (using list_object_versions)
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
        logger.info(f"Buckets tested: {len(test_buckets['all'])} ({len(test_buckets['plain'])} plain, {len(test_buckets['versioned'])} versioned)")
        logger.info("=" * 80)


if __name__ == "__main__":
    import sys
    exit_code = pytest.main([__file__, "-v", "-s"])
    sys.exit(exit_code)
