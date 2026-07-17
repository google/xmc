FROM python:3.11-slim
WORKDIR /app
COPY worker.py /app/worker.py
RUN chmod +x /app/worker.py
ENTRYPOINT ["python3", "-u", "/app/worker.py"]
