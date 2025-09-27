import pytest
import boto3
import requests
import time
import logging
import os


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


class TestS3BucketExporter:
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
    def create_test_buckets_and_files(self, s3_client):
        """Create test buckets and upload files, returning metadata for verification."""
        time.sleep(5)
        buckets = ["test-bucket-1", "test-bucket-2"]
        files = [
            {"bucket": "test-bucket-1", "key": "file1.txt", "content": "Hello World" * 100},
            {"bucket": "test-bucket-1", "key": "file2.txt", "content": "Test Content" * 50},
            {"bucket": "test-bucket-2", "key": "data.txt", "content": "Random Data" * 75},
        ]

        # Metadata for all buckets and files
        bucket_metadata = {}

        # Create buckets and upload files
        for bucket in buckets:
            s3_client.create_bucket(Bucket=bucket)
            logger.info(f"Bucket '{bucket}' created")
            bucket_metadata[bucket] = {"files": [], "total_size": 0}

        for file_info in files:
            bucket, key, content = file_info["bucket"], file_info["key"], file_info["content"]
            s3_client.put_object(Bucket=bucket, Key=key, Body=content.encode())
            logger.info(f"Uploaded file '{key}' to bucket '{bucket}'")

            # Update bucket metadata
            bucket_metadata[bucket]["files"].append({"key": key, "size": len(content)})
            bucket_metadata[bucket]["total_size"] += len(content)

        return bucket_metadata

    def fetch_metrics(self):
        """Fetch metrics from the S3 exporter."""
        try:
            logger.info(f"Fetching metrics from {S3_EXPORTER_URL}")
            response = requests.get(S3_EXPORTER_URL)
            response.raise_for_status()
            return response.text
        except requests.RequestException as e:
            logger.error(f"Failed to fetch metrics from exporter: {e}")
            raise

    def parse_metrics(self, metrics_text):
        """Parse metrics text into a structured dictionary."""
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

                elif 's3_total_object_number' in line:
                    storage_class = line.split('storageClass="')[1].split('"')[0]
                    total_objects = float(line.split()[-1])
                    parsed_metrics.setdefault("total", {}).setdefault("storage_classes", {}).setdefault(storage_class, {})["object_count"] = total_objects

                elif 's3_total_size' in line:
                    storage_class = line.split('storageClass="')[1].split('"')[0]
                    total_size = float(line.split()[-1])
                    parsed_metrics.setdefault("total", {}).setdefault("storage_classes", {}).setdefault(storage_class, {})["total_size"] = total_size

            except (IndexError, ValueError) as e:
                logger.warning(f"Error parsing metrics line: {line}. Error: {e}")
        return parsed_metrics

    def verify_bucket_metrics(self, bucket, metadata, bucket_metrics):
        """Verify object count and total size for a specific bucket."""
        if bucket not in bucket_metrics:
            raise AssertionError(f"Metrics for bucket '{bucket}' are missing")

        storage_class = "STANDARD"  # Assuming STANDARD storage class for test
        metrics = bucket_metrics[bucket]["storage_classes"][storage_class]

        # Verify object count
        actual_count = metrics["object_count"]
        expected_count = len(metadata["files"])
        assert actual_count == expected_count, (
            f"Bucket '{bucket}' object count mismatch. Expected: {expected_count}, Got: {actual_count}"
        )

        # Verify total size
        actual_size = metrics["total_size"]
        expected_size = metadata["total_size"]
        assert abs(actual_size - expected_size) < 10, (
            f"Bucket '{bucket}' size mismatch. Expected: {expected_size}, Got: {actual_size}"
        )

        logger.info(f"Metrics verified for bucket '{bucket}'")

    def verify_global_metrics(self, bucket_metadata, parsed_metrics):
        """Verify global metrics (total object count, total size, and endpoint status)."""
        storage_class = "STANDARD"  # Assuming STANDARD storage class for test
        total_metrics = parsed_metrics["total"]["storage_classes"][storage_class]

        total_objects_expected = sum(len(metadata["files"]) for metadata in bucket_metadata.values())
        total_size_expected = sum(metadata["total_size"] for metadata in bucket_metadata.values())

        # Verify total object count
        assert total_metrics["object_count"] == total_objects_expected, (
            f"Total object count mismatch. Expected: {total_objects_expected}, "
            f"Got: {total_metrics['object_count']}"
        )

        # Verify total size
        assert total_metrics["total_size"] == total_size_expected, (
            f"Total size mismatch. Expected: {total_size_expected}, "
            f"Got: {total_metrics['total_size']}"
        )

        # Verify endpoint status
        assert parsed_metrics["endpoint_up"] == 1, (
            f"Endpoint status mismatch. Expected: 1, Got: {parsed_metrics['endpoint_up']}"
        )

        logger.info("Global metrics verified successfully")

    def test_exporter_metrics(self, create_test_buckets_and_files):
        """End-to-end test for verifying exporter metrics."""
        time.sleep(10)  # Allow exporter time to collect metrics
        metrics_text = self.fetch_metrics()
        parsed_metrics = self.parse_metrics(metrics_text)

        # Verify bucket-specific metrics
        for bucket, metadata in create_test_buckets_and_files.items():
            self.verify_bucket_metrics(bucket, metadata, parsed_metrics)

        # Verify global metrics
        self.verify_global_metrics(create_test_buckets_and_files, parsed_metrics)

        logger.info("All tests passed successfully")


if __name__ == "__main__":
    import sys
    exit_code = pytest.main()
    sys.exit(exit_code)
