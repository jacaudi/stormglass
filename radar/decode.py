"""Decode NEXRAD Level 3 NIDS bytes into contoured GeoJSON isobands (Contract A)."""

import os
import tempfile
from typing import Any

os.environ.setdefault("MPLBACKEND", "Agg")

import matplotlib

matplotlib.use("Agg")
import geojsoncontour
import matplotlib.pyplot as plt
import numpy as np
import pyart
from pyart.io.nexrad_level3 import NEXRADLevel3File

# Isoband steps over the NWS reflectivity scale (dBZ), 5-dBZ bands.
_DBZ_LEVELS = list(range(5, 80, 5))


class DecodeError(Exception):
    """Raised when NIDS bytes cannot be decoded into a reflectivity field."""


def to_geojson(nids: bytes, product: str) -> dict[str, Any]:
    """Decode raw NEXRAD Level 3 NIDS bytes into a Contract-A GeoJSON FeatureCollection.

    Raises DecodeError if the bytes cannot be decoded or contoured.
    """
    with tempfile.NamedTemporaryFile(suffix=".nids") as tmp:
        tmp.write(nids)
        tmp.flush()

        try:
            radar = pyart.io.read_nexrad_level3(tmp.name)
        except Exception as exc:
            raise DecodeError(f"failed to decode NIDS bytes: {exc}") from exc

        try:
            site = _read_site_code(tmp.name)
        except Exception as exc:
            raise DecodeError(f"failed to read NIDS site header: {exc}") from exc

    try:
        feature_collection, bbox = _contour_reflectivity(radar)
        scan_time = pyart.util.datetime_from_radar(radar).isoformat() + "Z"
    except Exception as exc:
        raise DecodeError(f"failed to contour reflectivity field: {exc}") from exc

    feature_collection["metadata"] = {
        "site": site,
        "product": product,
        "scan_time": scan_time,
        "bbox": bbox,
    }
    return dict(feature_collection)


def _read_site_code(path: str) -> str:
    """Parse the 3-char site code from the NIDS text header's AWIPS ID (e.g. 'N0BTLX' -> 'TLX')."""
    nfile = NEXRADLevel3File(path)
    try:
        lines = [
            line
            for line in nfile.text_header.decode("ascii", "replace").splitlines()
            if line.strip()
        ]
        awips_id = lines[-1].strip()
        return awips_id[3:]
    finally:
        nfile.close()


def _contour_reflectivity(radar) -> tuple[dict[str, Any], list[float]]:
    field_name = next(iter(radar.fields))
    reflectivity = radar.fields[field_name]["data"]
    lon = np.asarray(radar.gate_longitude["data"]).ravel()
    lat = np.asarray(radar.gate_latitude["data"]).ravel()
    values = np.ma.filled(reflectivity, np.nan).ravel()

    valid = np.isfinite(values)
    lon, lat, values = lon[valid], lat[valid], values[valid]
    bbox = [float(lon.min()), float(lat.min()), float(lon.max()), float(lat.max())]

    fig, ax = plt.subplots()
    try:
        contour_set = ax.tricontourf(lon, lat, values, levels=_DBZ_LEVELS, extend="neither")
        feature_collection = geojsoncontour.contourf_to_geojson(
            contour_set, ndigits=5, serialize=False
        )
    finally:
        plt.close(fig)

    for feature in feature_collection["features"]:
        dbz_min, dbz_max = _parse_band(feature["properties"]["title"])
        feature["properties"]["dbz_min"] = dbz_min
        feature["properties"]["dbz_max"] = dbz_max

    return feature_collection, bbox


def _parse_band(title: str) -> tuple[int, int]:
    """Parse geojsoncontour's 'LOW.00-HIGH.00 ' title into integer 5-dBZ band bounds."""
    low_str, high_str = title.strip().split("-")
    # round() on a float already returns an int in Python 3, so the int() wrapper
    # this used to carry was a no-op (RUF046).
    return round(float(low_str)), round(float(high_str))
