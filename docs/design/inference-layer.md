## 8. Below gohan: inference layer

gohan never implements engine optimizations. It must not defeat them and must expose their knobs.

| Technique | Bottleneck | gohan responsibility |
|---|---|---|
| Prefix / prompt caching | TTFT | `StablePrefix` assembly, deterministic tool order, `CacheBreak`, no dynamic data in prefix, cached-token metrics |
| KV-cache locality across replicas | TTFT | `AffinityKey` (session/tenant hash) to gateway or engine router |
| Chunked prefill, continuous batching, PagedAttention | throughput | no client-side serialization or batching; bulkhead sized to engine capacity (`MaxInFlight`); queue-wait metric |
| Speculative decoding, weight/KV quantization, MoE | TPOT / throughput | engine config; gohan routes between variants via profiles + `Router` |
| Constrained decoding | extra round trips | `Constrained` structured output when `Caps.Constrained` |
| Priority scheduling | tail latency | `LatencyClass` → `Priority` |
| Decode length | TPOT × tokens | `MaxTokens` per flow and per turn |
| Batch APIs | cost | `AwaitingBatch` suspension |
| New engine features | – | `ModelOptions.Extra` passthrough |

Diagnosis loop: high TTFT → check cached-token ratio and affinity; high TPOT → route to faster variant or cap output; low throughput with good TTFT/TPOT → queue wait and bulkhead sizing.
