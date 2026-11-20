
### Administrator failure diagnostics

New upstream request failures and rejected client WebSocket handshakes are written to container/terminal output and administrator Runtime logs (module `gateway`, retained for seven days). Correlate `request_id` in the summary with request logs. Handshake rejections retain specific reasons such as connection limits. Failure summaries include account (the runtime log account column), attempt, transport, status, dispatch/completion/output observations and fixed failure categories.

Native WebSocket read failures include connection reuse, the last event category, a close code when available, and timeout/cancellation categories. Raw close reasons, upstream bodies, URLs, headers and credentials are excluded. Without a close frame, a read failure alone cannot establish the upstream or proxy's internal root cause. Records start after upgrading; historical failures cannot be reconstructed. For Docker Compose, use `docker compose logs --timestamps --tail 200 fiftyapi`.
