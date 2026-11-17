# Automatic settlement

[简体中文](../automatic-settlement.md)

| Condition | Local budget handling |
| --- | --- |
| Completed with reliable usage | Settle immutable request pricing rules using the served tier; release excess reservation |
| Interrupted or cancelled with reliable usage | Settle observed usage, release the reservation, and record the failure |
| Missing, incomplete, or invalid usage | Release reservation, retain pending evidence, and pause further calls by that user |
| Not dispatched or explicitly rejected before execution | Settle at zero and release reservation |
| Missing tier or cache price discovered after dispatch | Retain usage and snapshot, release reservation, and require administrator settlement |
| Ledger write failure | Retain evidence, mark pending, and attempt reservation release; startup recovery retries when database writes become available |

Raw tokens are never multiplied by pricing factors. The served tier takes precedence; absent upstream confirmation uses the requested tier and is labeled accordingly. Cached input is not charged twice, and one-hour cache writes have separate prices. Reliable usage requires complete nonnegative input/output counters; missing output is not assumed to be zero, and invalid samples do not fall back to earlier usage guesses.

Evidence is persisted before atomic ledger/log settlement. Settlement is idempotent, request prices are immutable, and administrator decisions are preserved. Pending charges retain their original 5-hour/7-day windows rather than charging new windows. Pending evidence is protected from normal log cleanup.

Startup recovery runs under the single-instance lock before serving traffic and never repeats model calls. Historical settled bills are not retroactively changed. Administrators can retry ledger settlement or enter a documented manual amount from request-log exceptions. Entering zero for unknown usage is an explicit administrator decision.

Verification uses simulated upstreams and temporary databases; no live-provider billing acceptance was run. See [low-maintenance sharing](low-maintenance.md) for full behavior and limits.
