# Eval report: golden (recall)

## Accuracy by category

| Category | memd |
|---|---|
| **overall** | **100.0% (10/10)** |
| presence | 100.0% (5/5) |
| presence-dated | 100.0% (3/3) |
| presence-updated | 100.0% (2/2) |

## Token usage per slot (input / output)

| Slot | memd |
|---|---|
| CONSOLIDATE | 60 / 30 |
| EXTRACT | 120 / 24 |

## Latency (ms)

| Operation | memd p50 | memd p95 | memd n |
|---|---|---|---|
| barrier | 604.1 | 604.2 | 3 |
| judge | 0.0 | 0.0 | 10 |
| recall | 0.7 | 1.3 | 10 |
| retain | 0.2 | 2.0 | 6 |

## Problems

- memd: 3 conversations, 0 failed background operations, 0 conversation errors
