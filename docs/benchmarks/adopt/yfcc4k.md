# YFCC4k Image Geolocation

**Family:** `adopt-geo` · **Reference:** YFCC4k benchmark from Vo et al. (ICCV 2017) and follow-ups

## What it measures

Median geodetic error (km) and threshold-accuracy at the standard IM2GPS grid: street (1km), city (25km), region (200km), country (750km), continent (2500km).

## Why we run it

Our `geo_vision`, `reverse_image_search`, and `exif_extract_geolocate` tools claim image-geolocation capability. YFCC4k is the standard benchmark; refusing to be measured on it means we're claiming capability without proof.

## Methodology

- **Dataset:** YFCC4k test split (4,536 images sampled from YFCC100M, with ground-truth lat/lng). Pinned by SHA-256.
- **Subset for adopt-tier:** `yfcc4k-100` — first 100 images (deterministic seed). Full-set runs are `BENCHMARK_HEAVY=1`.
- **Driver:** Two parallel drivers:
  1. `tool-driver` against `geo_vision` (vision-LLM path)
  2. `tool-driver` against `reverse_image_search` (search-then-localize path)
- **Scoring:** `haversineKm(predicted, ground_truth)`. Headline = median km. Threshold-accuracy reported in `score_breakdown`.

## Headline reference scores (median km, IM2GPS3k / YFCC4k)

| System | Median km |
|---|---|
| IM2GPS (2008, retrieval baseline) | ~1500 |
| PlaNet (2016, classification) | ~1100 |
| Vo et al. CNN (2017) | ~870 |
| GeoCLIP (2024) | ~440 |
| PIGEON / PIGEOTTO (2024) | ~210 |

Top-tier GeoGuessr humans cluster around 100–250 km median.

## What "winning" looks like

- **Floor:** median <1500 km (better than 2008 baseline). If we miss this, EXIF is broken or vision-LLM dispatch is broken.
- **Stretch:** median <500 km. Competitive with GeoCLIP — only realistic if the underlying vision LLM is strong.

## Sources

- [IM2GPS – CMU](http://graphics.cs.cmu.edu/projects/im2gps/)
- [Revisiting IM2GPS in the Deep Learning Era (ICCV 2017)](https://openaccess.thecvf.com/content_ICCV_2017/papers/Vo_Revisiting_IM2GPS_in_ICCV_2017_paper.pdf)
- [PIGEON](https://arxiv.org/html/2307.05845v5)
