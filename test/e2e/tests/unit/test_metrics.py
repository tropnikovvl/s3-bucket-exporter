from e2elib.metrics import parse_metrics

SAMPLE = """\
# HELP s3_endpoint_up Connection to S3 successful
# TYPE s3_endpoint_up gauge
s3_endpoint_up{s3Endpoint="http://floci:4566",s3Region="us-east-1"} 1
# TYPE s3_bucket_count gauge
s3_bucket_count{s3Endpoint="http://floci:4566",s3Region="us-east-1"} 3
# TYPE s3_bucket_object_number gauge
s3_bucket_object_number{s3Endpoint="e",s3Region="r",bucketName="b1",storageClass="STANDARD",versionStatus="current"} 2
s3_bucket_object_number{s3Endpoint="e",s3Region="r",bucketName="b1",storageClass="STANDARD",versionStatus="noncurrent"} 1
# TYPE s3_bucket_size gauge
s3_bucket_size{s3Endpoint="e",s3Region="r",bucketName="b1",storageClass="STANDARD",versionStatus="current"} 2048
# TYPE s3_bucket_delete_markers gauge
s3_bucket_delete_markers{s3Endpoint="e",s3Region="r",bucketName="b1"} 4
# TYPE s3_total_object_number gauge
s3_total_object_number{s3Endpoint="e",s3Region="r",storageClass="STANDARD",versionStatus="current"} 9
# TYPE s3_total_size gauge
s3_total_size{s3Endpoint="e",s3Region="r",storageClass="STANDARD",versionStatus="noncurrent"} 512
# TYPE s3_total_delete_markers gauge
s3_total_delete_markers{s3Endpoint="e",s3Region="r"} 4
"""


def test_parse_metrics_shape():
    m = parse_metrics(SAMPLE)
    assert m["endpoint_up"] == 1
    assert m["bucket_count"] == 3
    assert m["b1"]["storage_classes"]["STANDARD"]["object_count"]["current"] == 2
    assert m["b1"]["storage_classes"]["STANDARD"]["object_count"]["noncurrent"] == 1
    assert m["b1"]["storage_classes"]["STANDARD"]["total_size"]["current"] == 2048
    assert m["b1"]["delete_markers"] == 4
    assert m["total"]["storage_classes"]["STANDARD"]["object_count"]["current"] == 9
    assert m["total"]["storage_classes"]["STANDARD"]["total_size"]["noncurrent"] == 512
    assert m["total_delete_markers"] == 4


def test_parse_metrics_ignores_unrelated():
    m = parse_metrics("# HELP x foo\n# TYPE x gauge\nx 1\n")
    assert m == {}
