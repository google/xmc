FROM python:3.11-slim
WORKDIR /app
COPY counter.py /app/counter.py
RUN chmod +x /app/counter.py
ENTRYPOINT ["python3", "-u", "/app/counter.py"]
