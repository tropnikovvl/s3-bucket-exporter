"""Fetch and parse s3-bucket-exporter Prometheus metrics."""
import logging

import requests
from prometheus_client.parser import text_string_to_metric_families

logger = logging.getLogger(__name__)


def fetch_metrics(url: str, timeout: float = 5) -> str:
    """Fetch raw Prometheus metrics text from the exporter."""
    try:
        response = requests.get(url, timeout=timeout)
        response.raise_for_status()
        return response.text
    except requests.RequestException as e:
        logger.error(f"Failed to fetch metrics from exporter: {e}")
        raise


def _bucket_sc(out: dict, labels: dict) -> dict:
    """Return the storage-class sub-dict for a per-bucket sample."""
    bucket = out.setdefault(labels["bucketName"], {})
    classes = bucket.setdefault("storage_classes", {})
    return classes.setdefault(labels["storageClass"], {})


def _total_sc(out: dict, labels: dict) -> dict:
    """Return the storage-class sub-dict for a total sample."""
    classes = out.setdefault("total", {}).setdefault("storage_classes", {})
    return classes.setdefault(labels["storageClass"], {})


def parse_metrics(text: str) -> dict:
    """Parse exporter metrics into a nested dict keyed by bucket and 'total'."""
    out: dict = {}
    for family in text_string_to_metric_families(text):
        for sample in family.samples:
            name, labels, value = sample.name, sample.labels, sample.value

            if name == "s3_bucket_object_number":
                _bucket_sc(out, labels).setdefault("object_count", {})[labels["versionStatus"]] = value
            elif name == "s3_bucket_size":
                _bucket_sc(out, labels).setdefault("total_size", {})[labels["versionStatus"]] = value
            elif name == "s3_bucket_delete_markers":
                out.setdefault(labels["bucketName"], {})["delete_markers"] = value
            elif name == "s3_total_object_number":
                _total_sc(out, labels).setdefault("object_count", {})[labels["versionStatus"]] = value
            elif name == "s3_total_size":
                _total_sc(out, labels).setdefault("total_size", {})[labels["versionStatus"]] = value
            elif name == "s3_total_delete_markers":
                out["total_delete_markers"] = value
            elif name == "s3_endpoint_up":
                out["endpoint_up"] = value
            elif name == "s3_bucket_count":
                out["bucket_count"] = value
    return out
