# Hybrid Logical Clock schema v1

`beresta.hlc.v1` is the closed map:

| Key | Type | Rule |
|---|---|---|
| `physical_ms` | `uint64` | Unix epoch milliseconds; subject to the configured future-skew limit |
| `logical` | `uint32` | Incremented when physical time does not advance; overflow prevents a new event until time advances |
| `device_id` | `bytes(16)` | Valid v1 `device_id` |

Total ordering is lexicographic by `(physical_ms, logical, device_id bytes)`. The tuple is persisted atomically with emitted or applied operations.
