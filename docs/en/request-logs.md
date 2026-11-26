
### Administrator failure diagnostics

Members can view and copy the request ID in **Request diagnostics** when reporting an issue to an administrator. The administrator log list includes a request ID column. Settlement details show a reason when one is recorded.

Administrators can open matching request logs for the selected dashboard time range by selecting a failure group. Groups use diagnostic cause, protocol, and upstream account.

A WebSocket handshake rejected with HTTP 429 at the per-user connection limit records the connection count and configured limit at the time of rejection. `HTTP JSON` in the list describes the handshake error response.

New upstream request failures and rejected client WebSocket handshakes are written to container/terminal output and administrator Runtime logs (module `gateway`, retained for seven days). Correlate `request_id` in the summary with request logs. Handshake rejections retain specific reasons such as connection limits. Failure summaries include account (the runtime log account column), attempt, transport, status, dispatch/completion/output observations and fixed failure categories.

Native WebSocket read failures include connection reuse, the last event category, a close code when available, and timeout/cancellation categories. Raw close reasons, upstream bodies, URLs, headers and credentials are excluded. Without a close frame, a read failure alone cannot establish the upstream or proxy's internal root cause. Records start after upgrading; historical failures cannot be reconstructed. For Docker Compose, use `docker compose logs --timestamps --tail 200 fiftyapi`.

The admin dashboard shows approximate first-output P50, P95, and sample counts for SSE and WebSocket over the last hour. Timing starts at request arrival. Minute buckets are kept in memory and reset on restart.
