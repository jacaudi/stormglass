# Py-ART pulls in cartopy, which ships no prebuilt wheel for this platform and
# must be compiled against GEOS/PROJ. Build in one stage with the toolchain,
# then copy just the venv into a clean runtime stage so the final image
# doesn't carry gcc/g++/dev headers.
FROM python:3.14-slim AS builder

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential \
        libgeos-dev \
        libproj-dev \
    && rm -rf /var/lib/apt/lists/*

RUN python -m venv /venv
ENV PATH="/venv/bin:$PATH"

WORKDIR /app
COPY requirements.txt .
# pytest is a test-only dep pinned in requirements.txt (shared with the test
# suite); drop it here so it doesn't ship in the runtime image.
RUN pip install --no-cache-dir -r requirements.txt \
    && pip uninstall -y --no-input pytest

FROM python:3.14-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        libgeos-c1t64 \
        libproj25 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd -m -u 10001 appuser

COPY --from=builder /venv /venv
ENV PATH="/venv/bin:$PATH"

WORKDIR /app
COPY app.py decode.py ./
RUN chown -R appuser:appuser /app
USER appuser

ENV MPLBACKEND=Agg

EXPOSE 8081

# No curl/wget in slim; probe /healthz with the stdlib instead.
HEALTHCHECK --interval=30s --timeout=3s --start-period=40s --retries=3 \
    CMD ["python", "-c", "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://127.0.0.1:8081/healthz').status==200 else 1)"]

CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8081"]
