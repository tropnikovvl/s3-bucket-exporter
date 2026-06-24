"""Read the actual S3 bucket state via list_object_versions."""
import logging
from typing import Dict, List

logger = logging.getLogger(__name__)


def get_actual_bucket_state(s3_client, buckets: List[str]) -> Dict[str, Dict]:
    """Return per-bucket current/noncurrent counts+sizes and delete-marker counts.

    Errors from S3 propagate so a listing failure cannot masquerade as an
    all-zero state.
    """
    state: Dict[str, Dict] = {}
    for bucket in buckets:
        current_count = current_size = 0
        noncurrent_count = noncurrent_size = 0
        delete_markers = 0

        paginator = s3_client.get_paginator("list_object_versions")
        for page in paginator.paginate(Bucket=bucket):
            for ver in page.get("Versions", []):
                if ver.get("IsLatest", False):
                    current_count += 1
                    current_size += ver["Size"]
                else:
                    noncurrent_count += 1
                    noncurrent_size += ver["Size"]
            delete_markers += len(page.get("DeleteMarkers", []))

        state[bucket] = {
            "current_count": current_count,
            "current_size": current_size,
            "noncurrent_count": noncurrent_count,
            "noncurrent_size": noncurrent_size,
            "delete_markers": delete_markers,
        }
    return state
