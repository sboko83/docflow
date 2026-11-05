# Benchmark trend

Generated from `runs.jsonl`. Rows are newest first; deltas compare with the previous row for the same task and arm.

## hard

| Date | docflow | Arm | Oracle | Δ oracle | Cost | Δ cost | Minutes | Attempts | Replans | Task |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|
| 2026-11-05 | 0.24.0 (f3bbc87) / f3bbc87a | single | 100% | +0 pp | 0.15 | — | 16.5 | 0 | 0 | 1b5dc8b9 |
| 2026-11-05 | 0.24.0 (f5950d0) / f5950d0b | docflow | 100% | — | 0.16 | — | 29.0 | 5 | 0 | 1b5dc8b9 |
| 2026-11-05 | 0.24.0 (f5950d0) / f5950d0b | single | 100% | — | — | — | 19.6 | 0 | 0 | 1b5dc8b9 |

## large

| Date | docflow | Arm | Oracle | Δ oracle | Cost | Δ cost | Minutes | Attempts | Replans | Task |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|
| 2026-11-01 | 0.21.0 (7841550) / 7841550f | docflow | 100% | +0 pp | 0.30 | -0.75 | 75.1 | 8 | 0 | 4fdbd18e |
| 2026-11-01 | 0.21.0 (08bd6f6+dirty) / 08bd6f62 | single | 100% | +0 pp | 1.75 | — | 17.3 | 0 | 0 | 4fdbd18e |
| 2026-10-31 | 0.21.0 (de73ef9) / de73ef9d | docflow-one | 100% | — | — | — | 21.8 | 11 | 1 | 4fdbd18e |
| 2026-10-31 | 0.21.0 (15989ef) / 15989ef4 | single | 100% | — | — | — | 7.6 | 0 | 0 | 4fdbd18e |
| 2026-10-31 | 0.21.0 (e9dbc1b) / e9dbc1b9 | docflow | 100% | — | 1.05 | — | 56.4 | 8 | 0 | 4fdbd18e |

## medium

| Date | docflow | Arm | Oracle | Δ oracle | Cost | Δ cost | Minutes | Attempts | Replans | Task |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|
| 2026-11-01 | 0.21.0 (5ab9fe8) / 5ab9fe83 | docflow | 100% | +0 pp | 0.12 | -0.29 | 26.9 | 4 | 0 | 4d0c4dc9 |
| 2026-11-01 | 0.21.0 (9ebc1d6+dirty) / 9ebc1d66 | single | 100% | +0 pp | 1.16 | — | 15.6 | 0 | 0 | 4d0c4dc9 |
| 2026-10-31 | 0.21.0 (7189310) / 71893103 | docflow-one | 100% | +0 pp | — | — | 11.1 | 7 | 1 | 4d0c4dc9 |
| 2026-10-31 | 0.21.0 (1072799) / 10727993 | docflow-one | 100% | — | — | — | 12.6 | 7 | 1 | 4d0c4dc9 |
| 2026-10-31 | 0.21.0 (1072799) / 10727993 | docflow | 100% | — | 0.41 | — | 18.6 | 4 | 0 | 4d0c4dc9 |
| 2026-10-31 | 0.21.0 (1072799) / 10727993 | single | 100% | — | — | — | 6.3 | 0 | 0 | 4d0c4dc9 |

## small

| Date | docflow | Arm | Oracle | Δ oracle | Cost | Δ cost | Minutes | Attempts | Replans | Task |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|
| 2026-11-01 | 0.21.0 (5515616) / 5515616e | docflow | 100% | +0 pp | 0.03 | -0.00 | 7.3 | 1 | 0 | d4152bf2 |
| 2026-11-01 | 0.21.0 (563bb89+dirty) / 563bb892 | single | 100% | +0 pp | 0.24 | — | 3.5 | 0 | 0 | d4152bf2 |
| 2026-10-31 | 0.21.0 (92e4dbe) / 92e4dbe2 | single | 100% | +73 pp | — | — | 2.1 | 0 | 0 | d4152bf2 |
| 2026-10-31 | 0.21.0 (c935999) / c9359994 | docflow-one | 100% | — | — | — | 2.0 | 1 | 0 | d4152bf2 |
| 2026-10-31 | 0.21.0 (c935999) / c9359994 | docflow | 100% | — | 0.03 | — | 4.6 | 1 | 0 | d4152bf2 |
| 2026-10-31 | 0.21.0 (c935999) / c9359994 | single | 27% | — | — | — | 0.0 | 0 | 0 | d4152bf2 |
