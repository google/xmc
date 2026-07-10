FROM python:3.11-slim
WORKDIR /app
COPY init_step.py /app/init_step.py
COPY worker.py /app/worker.py
RUN chmod +x /app/init_step.py /app/worker.py
ENTRYPOINT ["python3", "-u", "/app/worker.py"]
