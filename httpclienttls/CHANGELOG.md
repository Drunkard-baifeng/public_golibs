# Changelog

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
