FROM python:3.13.1-slim

WORKDIR /app

COPY requirements.txt pytest.ini .

RUN pip install -r requirements.txt

COPY tests/ ./tests/

CMD ["pytest", "-v", "/app/tests/test_s3_bucket_exporter.py"]
