# Document conversion service: FastAPI + Docling with CPU-only inference.
FROM python:3.12-slim-bookworm

# System libraries the vision models and image decoders need, plus Tesseract so
# it is available as a selectable OCR engine alongside RapidOCR and EasyOCR.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        libgl1 \
        libglib2.0-0 \
        libgomp1 \
        tesseract-ocr \
        tesseract-ocr-eng \
        curl \
    && rm -rf /var/lib/apt/lists/*

# Run as a non-root user with a stable home, because that is where Docling
# caches its model weights.
RUN useradd --create-home --shell /bin/bash app
ENV HOME=/home/app \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    HF_HOME=/home/app/.cache/huggingface \
    TORCH_HOME=/home/app/.cache/torch \
    OMP_NUM_THREADS=4 \
    CONVERTER_HOST=0.0.0.0 \
    CONVERTER_PORT=5001

WORKDIR /app

# Install dependencies first so code edits do not invalidate the heavy layer.
# Torch comes from the CPU-only index, which removes roughly 2GB of CUDA wheels.
COPY requirements.txt ./
RUN pip install --no-cache-dir --extra-index-url https://download.pytorch.org/whl/cpu \
        -r requirements.txt

# Bake the layout, table and OCR model weights into the image so the first
# upload does not stall on a download, and so the service works offline.
USER app
RUN docling-tools models download

USER root
COPY --chown=app:app app ./app
USER app

EXPOSE 5001

# 127.0.0.1 rather than localhost: uvicorn binds IPv4 only, and "localhost"
# resolves to ::1 first inside the container.
HEALTHCHECK --interval=15s --timeout=5s --retries=20 --start-period=90s \
    CMD curl -fsS http://127.0.0.1:5001/health || exit 1

CMD ["sh", "-c", "uvicorn app.main:app --host ${CONVERTER_HOST} --port ${CONVERTER_PORT} --timeout-keep-alive 75"]
