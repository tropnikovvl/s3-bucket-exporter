FROM python:3.13.1-slim

WORKDIR /app

COPY requirements.txt pytest.ini .

RUN pip install -r requirements.txt

COPY tests/ ./tests/

ENV TEST_PROFILE=short

CMD pytest -v /app/tests/test_${TEST_PROFILE}_running_e2e.py
