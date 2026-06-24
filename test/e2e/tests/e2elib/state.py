"""Read the actual S3 bucket state via list_object_versions, by storage class."""
import logging
from typing import Dict, List

logger = logging.getLogger(__name__)


def class_metrics(current_count: int = 0, current_size: int = 0,
                  noncurrent_count: int = 0, noncurrent_size: int = 0) -> Dict[str, int]:
    """Per-storage-class counter dict — the shared shape used by the state
    reader, the verifier, and the short suite's literal oracle."""
    return {"current_count": current_count, "current_size": current_size,
            "noncurrent_count": noncurrent_count, "noncurrent_size": noncurrent_size}


def get_actual_bucket_state(s3_client, buckets: List[str]) -> Dict[str, Dict]:
    """Return per-bucket per-storage-class current/noncurrent counts+sizes and
    bucket-level delete-marker counts.

    Errors from S3 propagate so a listing failure cannot masquerade as an
    all-zero state.
    """
    state: Dict[str, Dict] = {}
    for bucket in buckets:
        classes: Dict[str, Dict[str, int]] = {}
        delete_markers = 0

        paginator = s3_client.get_paginator("list_object_versions")
        for page in paginator.paginate(Bucket=bucket):
            for ver in page.get("Versions", []):
                sc = ver.get("StorageClass", "STANDARD")
                bucket_sc = classes.setdefault(sc, class_metrics())
                if ver.get("IsLatest", False):
                    bucket_sc["current_count"] += 1
                    bucket_sc["current_size"] += ver["Size"]
                else:
                    bucket_sc["noncurrent_count"] += 1
                    bucket_sc["noncurrent_size"] += ver["Size"]
            delete_markers += len(page.get("DeleteMarkers", []))

        state[bucket] = {"storage_classes": classes, "delete_markers": delete_markers}
    return state
