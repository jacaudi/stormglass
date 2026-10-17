"""FastAPI radar sidecar: fetch newest NEXRAD Level 3 NIDS from S3, decode to
contoured GeoJSON isobands, and serve per Contract A (see design doc §9)."""

import datetime
import os

os.environ.setdefault("MPLBACKEND", "Agg")

import boto3
from botocore import UNSIGNED
from botocore.config import Config
from fastapi import FastAPI
from fastapi.responses import JSONResponse

import decode

_BUCKET = "unidata-nexrad-level3"

app = FastAPI()
_s3 = boto3.client("s3", config=Config(signature_version=UNSIGNED))


@app.get("/healthz")
def healthz():
    return {"status": "ok"}


@app.get("/radar")
def radar(site: str, product: str):
    site = site.upper()

    try:
        key = _fetch_newest_key(site, product)
    except Exception:
        return JSONResponse(status_code=500, content={"error": "internal"})

    if key is None:
        return JSONResponse(status_code=503, content={"error": "no_recent_scan"})

    try:
        nids = _s3.get_object(Bucket=_BUCKET, Key=key)["Body"].read()
    except Exception:
        return JSONResponse(status_code=500, content={"error": "internal"})

    try:
        return decode.to_geojson(nids, product)
    except decode.DecodeError:
        return JSONResponse(status_code=502, content={"error": "decode_failed"})
    except Exception:
        return JSONResponse(status_code=500, content={"error": "internal"})


def _fetch_newest_key(site: str, product: str) -> str | None:
    """Find the newest NIDS object key for site/product.

    Keys are flat `SSS_PPP_YYYY_MM_DD_HH_MM_SS`, real-time only (no reverse-listing
    API), so scan today's UTC date prefix, falling back to yesterday's to cover the
    UTC day-boundary edge case, rather than paginating the bucket's full history.
    """
    now = datetime.datetime.now(datetime.UTC)
    for day in (now, now - datetime.timedelta(days=1)):
        prefix = f"{site}_{product}_{day:%Y_%m_%d}"
        resp = _s3.list_objects_v2(Bucket=_BUCKET, Prefix=prefix)
        keys = [obj["Key"] for obj in resp.get("Contents", [])]
        if keys:
            return max(keys)
    return None
