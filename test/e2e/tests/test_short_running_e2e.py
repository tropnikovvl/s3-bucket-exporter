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

    @pytest.fixture(scope="class")
    def create_versioned_bucket(self, s3_client):
        """Create a versioned bucket with multiple versions and delete markers."""
        time.sleep(5)

        bucket = "test-bucket-versioned"
        s3_client.create_bucket(Bucket=bucket)
        logger.info(f"Bucket '{bucket}' created")

        # Enable versioning
        s3_client.put_bucket_versioning(
            Bucket=bucket,
            VersioningConfiguration={"Status": "Enabled"},
        )
        logger.info(f"Versioning enabled on '{bucket}'")

        # Upload v1 of a file (will become noncurrent after v2 upload)
        v1_content = "version one content"
        s3_client.put_object(Bucket=bucket, Key="versioned.txt", Body=v1_content.encode())
        logger.info("Uploaded versioned.txt v1")

        # Upload v2 of the same file (v1 becomes noncurrent)
        v2_content = "version two content updated"
        s3_client.put_object(Bucket=bucket, Key="versioned.txt", Body=v2_content.encode())
        logger.info("Uploaded versioned.txt v2 (v1 is now noncurrent)")

        # Upload a file and then delete it (creates a delete marker)
        delete_content = "this will be deleted"
        s3_client.put_object(Bucket=bucket, Key="to-delete.txt", Body=delete_content.encode())
        logger.info("Uploaded to-delete.txt")
        s3_client.delete_object(Bucket=bucket, Key="to-delete.txt")
        logger.info("Deleted to-delete.txt (delete marker created)")

        # Upload a regular file (only current version)
        regular_content = "regular file no versions"
        s3_client.put_object(Bucket=bucket, Key="regular.txt", Body=regular_content.encode())
        logger.info("Uploaded regular.txt")

        return {
            "bucket": bucket,
            "current_count": 2,  # versioned.txt (v2) + regular.txt
            "current_size": len(v2_content) + len(regular_content),
            "noncurrent_count": 2,  # versioned.txt (v1) + to-delete.txt (original)
            "noncurrent_size": len(v1_content) + len(delete_content),
            "delete_markers": 1,  # to-delete.txt delete marker
        }

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

                elif 's3_endpoint_up' in line:
                    endpoint_up = float(line.split()[-1])
                    parsed_metrics["endpoint_up"] = endpoint_up

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

                elif 's3_bucket_count' in line:
                    bucket_count = float(line.split()[-1])
                    parsed_metrics["bucket_count"] = bucket_count

            except (IndexError, ValueError) as e:
                logger.warning(f"Error parsing metrics line: {line}. Error: {e}")
        return parsed_metrics

    def verify_bucket_metrics(self, bucket, metadata, bucket_metrics):
        """Verify object count and total size for a specific bucket."""
        if bucket not in bucket_metrics:
            raise AssertionError(f"Metrics for bucket '{bucket}' are missing")

        storage_class = "STANDARD"
        metrics = bucket_metrics[bucket]["storage_classes"][storage_class]

        # Verify current object count
        actual_count = metrics.get("object_count", {}).get("current", 0)
        expected_count = len(metadata["files"])
        assert actual_count == expected_count, (
            f"Bucket '{bucket}' current object count mismatch. Expected: {expected_count}, Got: {actual_count}"
        )

        # Verify current total size
        actual_size = metrics.get("total_size", {}).get("current", 0)
        expected_size = metadata["total_size"]
        assert actual_size == expected_size, (
            f"Bucket '{bucket}' current size mismatch. Expected: {expected_size}, Got: {actual_size}"
        )

        # Verify noncurrent is zero for non-versioned buckets
        actual_noncurrent_count = metrics.get("object_count", {}).get("noncurrent", 0)
        assert actual_noncurrent_count == 0, (
            f"Bucket '{bucket}' noncurrent object count should be 0. Got: {actual_noncurrent_count}"
        )

        actual_noncurrent_size = metrics.get("total_size", {}).get("noncurrent", 0)
        assert actual_noncurrent_size == 0, (
            f"Bucket '{bucket}' noncurrent size should be 0. Got: {actual_noncurrent_size}"
        )

        # Verify delete markers is zero for non-versioned buckets
        actual_dm = bucket_metrics[bucket].get("delete_markers", 0)
        assert actual_dm == 0, (
            f"Bucket '{bucket}' delete markers should be 0. Got: {actual_dm}"
        )

        logger.info(f"Metrics verified for bucket '{bucket}'")

    def verify_global_metrics(self, bucket_metadata, parsed_metrics, extra_current_count=0, extra_current_size=0, extra_noncurrent_count=0, extra_noncurrent_size=0):
        """Verify global metrics (total object count, total size, and endpoint status)."""
        storage_class = "STANDARD"
        total_metrics = parsed_metrics["total"]["storage_classes"][storage_class]

        total_objects_expected = sum(len(metadata["files"]) for metadata in bucket_metadata.values()) + extra_current_count
        total_size_expected = sum(metadata["total_size"] for metadata in bucket_metadata.values()) + extra_current_size

        # Verify total current object count
        actual_current_objects = total_metrics.get("object_count", {}).get("current", 0)
        assert actual_current_objects == total_objects_expected, (
            f"Total current object count mismatch. Expected: {total_objects_expected}, "
            f"Got: {actual_current_objects}"
        )

        # Verify total current size
        actual_current_size = total_metrics.get("total_size", {}).get("current", 0)
        assert actual_current_size == total_size_expected, (
            f"Total current size mismatch. Expected: {total_size_expected}, "
            f"Got: {actual_current_size}"
        )

        # Verify noncurrent totals
        actual_noncurrent_objects = total_metrics.get("object_count", {}).get("noncurrent", 0)
        assert actual_noncurrent_objects == extra_noncurrent_count, (
            f"Total noncurrent object count mismatch. Expected: {extra_noncurrent_count}, "
            f"Got: {actual_noncurrent_objects}"
        )

        actual_noncurrent_size = total_metrics.get("total_size", {}).get("noncurrent", 0)
        assert actual_noncurrent_size == extra_noncurrent_size, (
            f"Total noncurrent size mismatch. Expected: {extra_noncurrent_size}, "
            f"Got: {actual_noncurrent_size}"
        )

        # Verify endpoint status
        assert parsed_metrics["endpoint_up"] == 1, (
            f"Endpoint status mismatch. Expected: 1, Got: {parsed_metrics['endpoint_up']}"
        )

        # Verify bucket count
        expected_bucket_count = len(bucket_metadata) + (1 if extra_current_count > 0 or extra_noncurrent_count > 0 else 0)
        assert parsed_metrics["bucket_count"] == expected_bucket_count, (
            f"Bucket count mismatch. Expected: {expected_bucket_count}, "
            f"Got: {parsed_metrics['bucket_count']}"
        )

        logger.info("Global metrics verified successfully")

    def verify_versioned_bucket_metrics(self, versioned_meta, parsed_metrics):
        """Verify metrics for the versioned bucket."""
        bucket = versioned_meta["bucket"]
        if bucket not in parsed_metrics:
            raise AssertionError(f"Metrics for versioned bucket '{bucket}' are missing")

        storage_class = "STANDARD"
        metrics = parsed_metrics[bucket]["storage_classes"][storage_class]

        # Verify current versions
        actual_current_count = metrics.get("object_count", {}).get("current", 0)
        assert actual_current_count == versioned_meta["current_count"], (
            f"Versioned bucket current object count mismatch. "
            f"Expected: {versioned_meta['current_count']}, Got: {actual_current_count}"
        )

        actual_current_size = metrics.get("total_size", {}).get("current", 0)
        assert actual_current_size == versioned_meta["current_size"], (
            f"Versioned bucket current size mismatch. "
            f"Expected: {versioned_meta['current_size']}, Got: {actual_current_size}"
        )

        # Verify noncurrent versions
        actual_noncurrent_count = metrics.get("object_count", {}).get("noncurrent", 0)
        assert actual_noncurrent_count == versioned_meta["noncurrent_count"], (
            f"Versioned bucket noncurrent object count mismatch. "
            f"Expected: {versioned_meta['noncurrent_count']}, Got: {actual_noncurrent_count}"
        )

        actual_noncurrent_size = metrics.get("total_size", {}).get("noncurrent", 0)
        assert actual_noncurrent_size == versioned_meta["noncurrent_size"], (
            f"Versioned bucket noncurrent size mismatch. "
            f"Expected: {versioned_meta['noncurrent_size']}, Got: {actual_noncurrent_size}"
        )

        # Verify delete markers
        actual_delete_markers = parsed_metrics[bucket].get("delete_markers", 0)
        assert actual_delete_markers == versioned_meta["delete_markers"], (
            f"Versioned bucket delete markers mismatch. "
            f"Expected: {versioned_meta['delete_markers']}, Got: {actual_delete_markers}"
        )

        logger.info(f"Versioned bucket metrics verified for '{bucket}'")

    def test_exporter_metrics(self, create_test_buckets_and_files, create_versioned_bucket):
        """End-to-end test for verifying exporter metrics."""
        time.sleep(10)  # Allow exporter time to collect metrics
        metrics_text = self.fetch_metrics()
        parsed_metrics = self.parse_metrics(metrics_text)

        # Verify bucket-specific metrics (non-versioned buckets)
        for bucket, metadata in create_test_buckets_and_files.items():
            self.verify_bucket_metrics(bucket, metadata, parsed_metrics)

        # Verify versioned bucket metrics
        v = create_versioned_bucket
        self.verify_versioned_bucket_metrics(v, parsed_metrics)

        # Verify global metrics (include versioned bucket contributions)
        self.verify_global_metrics(
            create_test_buckets_and_files,
            parsed_metrics,
            extra_current_count=v["current_count"],
            extra_current_size=v["current_size"],
            extra_noncurrent_count=v["noncurrent_count"],
            extra_noncurrent_size=v["noncurrent_size"],
        )

        # Verify total delete markers
        assert parsed_metrics.get("total_delete_markers", 0) == v["delete_markers"], (
            f"Total delete markers mismatch. Expected: {v['delete_markers']}, "
            f"Got: {parsed_metrics.get('total_delete_markers', 0)}"
        )

        logger.info("All tests passed successfully")


if __name__ == "__main__":
    import sys
    exit_code = pytest.main()
    sys.exit(exit_code)
