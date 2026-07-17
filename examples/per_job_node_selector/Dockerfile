FROM python:3.11-slim

WORKDIR /app

COPY coordinator.py /app/coordinator.py
COPY worker.py /app/worker.py

RUN chmod +x /app/coordinator.py /app/worker.py

ENTRYPOINT ["python3"]
