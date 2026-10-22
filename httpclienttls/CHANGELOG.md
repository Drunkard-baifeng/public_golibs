# Changelog

## v1.1.1 — 2026-10-23

### Fixed

- Close cancellable reader uploads on header/total timeout, parent cancellation, and response-body close, including full-duplex HTTP/1.1 and HTTP/2 requests.
- Make upload-body closure idempotent under repeated and concurrent transport cleanup.
- Reject known upload-size mismatches before dispatch and surface short or oversized streamed bodies with positive Content-Length on HTTP/1.1 and HTTP/2.
- Preserve UTF-8 multipart field/file names with quoted form-data parameters and reject CR/LF in part headers before starting an upload.
- Honor explicit empty browser headers, normalized browser modes, and explicit header-order overrides across redirects.
- Keep Fetch Metadata tied to the original initiator and full redirect chain, normalize default ports, and refresh generated Client Hints/Fetch Metadata for each destination's trust boundary.
- Preserve invalid proxy configuration errors when changing TLS verification; direct requests require explicitly fixing or clearing the proxy.
- Omit proxy credentials from URL-parse errors.
- Prevent responses from requests started before ClearCookies from repopulating the new observed-cookie snapshot.
- Accept informational CONNECT responses before the final success status, sharing one header-size budget and handshake deadline.

### Changed

- Remove duplicate cookie-jar initialization, multipart cancellation/cleanup, and per-hop request URL parsing.
- Normalize browser modes once, use canonical header-name lookups, and allocate explicit redirect-header state only when needed.
- Add a reproducible browser-header setup benchmark and static-analysis checks.

### Known Issues

- The current fhttp HTTP/2 transport can wait for a blocked upload reader after an early 3xx/4xx/5xx response. Keep a total/header/context deadline for such streams. A bounded-timeout regression runs normally; the failing upstream reproducer is opt-in with `HTTPCLIENTTLS_UPSTREAM_TEST=1`.

## v1.1.0 — 2026-10-23

### Added

- Live response streaming via `DoStream`, `StreamResponse`, and bounded `CopyTo`.
- Streaming multipart uploads, `PostMultipartStream`, and `PostMultipartContext`.
- Per-request total-timeout opt-out, response-header/read-idle timeouts, and explicit reader upload lengths.
- Context-aware HTTP(S)/SOCKS proxy tunnels with an independent handshake timeout.

### Fixed

- Join expired timeout callbacks before returning from a response-header wait or body read.
- Close reader uploads when request preparation fails.
- Drop request bodies on 301/302/303 redirects, including GET/HEAD requests.
- Enforce public-suffix restrictions on all cookie jars, including after reset.
- Keep encoded request Content-Type consistent when multi-value headers override it.
- Avoid integer overflow when the buffered response limit is `math.MaxInt64`.
- Accept all successful 2xx CONNECT responses and retain buffered tunnel data.
- Preserve signed URL query bytes when no query modifications are requested.
- Strip explicit credentials on cross-origin redirects and disable HTTP/3 when a TCP proxy is configured.

### Compatibility

- Existing buffered request APIs remain available with their response size limit.
- Multipart uploads now stream and are not automatically replayed on 307/308.
- Long-lived streams must explicitly disable or extend the default 30-second total timeout and close their response bodies.
