# Changelog

## v1.2.0 — 2026-11-09

### Fixed

- Release blocked HTTP/2 uploads after successful response EOF, response close, connection loss and cancellation, including HEAD. Clean up unfinished upload streams even when writer completion wins the response-completion race.
- Restrict optional protocol racing to bodyless GET/HEAD, preventing duplicate unsafe operations and shared upload readers. Clone candidates, honor caller cancellation, close losing responses, retain the winning streaming context, and reuse the actual HTTP/3 transport. Return cached-protocol request failures without replaying the request.
- Close unclaimed negotiated TCP/TLS connections on request cancellation and idle-connection cleanup; stop their cancellation callback when transport ownership transfers.
- Return an explicit error and release bodies after a caught synchronous transport panic instead of returning a misleading `(nil, nil)`.

- Fix blocked HTTP/2 uploads after early 3xx/4xx/5xx responses by closing the request body before joining the writer, including fixed-length EOF probes and delayed `100-continue` writers. Preserve 1xx/2xx full duplex, streaming response bodies and connection reuse.
- Prioritize intentional upload cancellation over close-induced read errors. Half-close unknown-length uploads without resetting the response; clean up incomplete known-length uploads after response completion without violating Content-Length.
- Make HTTP/2 request-body closure once-only and repair cancellation/reset cleanup, including canceled writers that never start.
- Ship the patched transport as internal source snapshots of fhttp v0.6.8 and tls-client v1.15.1. Keep public APIs, dependency versions, browser presets and TLS handshakes unchanged; no downstream replace directive is required.
- Enable the previously skipped redirect/upload reproducer and add streaming status, EOF-probe, continue, strict frame-level and upstream HTTP/2 wire-parity regressions.

### Added

- Add `Options.UserActivated` for programmatic navigation requests, preserving explicit headers and redirect behavior with legacy defaults unchanged.
- Add URL-scoped, detached jar cookie snapshots via `CookiesForURL` and `CookieHeader`, retaining duplicate names, request ordering and Secure/path/domain filtering.
- Add opt-in per-origin HTTP error cooldowns (429/503 by default), typed local rejection errors and observable expiry. Support bounded Retry-After seconds/HTTP dates, selected status codes and concurrent observations without automatic retries or sleeps.
- Validate cookie helper target URLs consistently and redact URL credentials/query details from invalid-target errors.
- Add opt-in HTTPS `Accept-CH` negotiation with bounded, origin-scoped TTL caches, preference introspection/clearing, explicit-header priority and a single bodyless-GET `Critical-CH` retry budget.
- Add immutable document `ClientHintContext` snapshots of Accept-CH and Permissions-Policy for same-origin and delegated subresources; document acceptance, not destination cache warm-up, controls generated hints. Subresource context responses do not mutate navigation preferences or trigger retries.
- Add `Sec-CH-UA-Full-Version`, `Sec-CH-UA-WoW64` and `Sec-CH-UA-Form-Factors` for the supported 64-bit desktop Chromium identities, including strict validation and ordered headers.
- Register upstream legacy desktop `safari_15_6_1` and `safari_16_0` presets as explicit choices; keep the default random pool unchanged and pin Safari identities to their native platform.
- Clear negotiated hint preferences together with cookies and isolate late in-flight responses from replacement stores. Reject scalar-string origin policy grants; origin strings require inner lists.
- Add HTTPS HTTP/1.1 and HTTP/2 negotiation, origin/port isolation, delegation, cache clearing/concurrency, suppressed/unsafe retry, streaming-body ownership, cancellation and timeout tests.
- Add opt-in `ExitLocale` lookup through the existing transport/proxy, with lazy automatic language selection, `Locale()` metadata and `RefreshLocale(ctx)` prewarming/refresh.
- Add provider failover, total probe deadlines, bounded JSON parsing, IANA timezone validation, transport-scoped success/failure TTL caches and concurrent lookup coalescing.
- Preserve explicit language/header suppression and isolate lookup traffic from business headers/cookies. Proxy reconfiguration discards old-route metadata without allowing direct fallback.
- Add optional `BrowserIdentity` for Windows/macOS/Linux HTTP metadata on existing desktop profiles, with compatible platform validation and immutable configuration snapshots.
- Add explicit high-entropy Client Hints generation from caller-supplied full browser/OS versions, architecture and bitness; preserve reduced UA tokens, explicit header overrides and redirect trust boundaries.
- Add `FingerprintSeed` for deterministic profile selection from a normalized, sorted pool without changing cryptographic TLS randomness.
- Filter incompatible entries from the implicit profile pool and validate all explicitly configured pool entries before selection.
- Add opt-in `StrictFingerprint` checks for configured UA and managed Client Hints before network dispatch; reject non-empty unmodeled UA hints rather than passing unchecked identity claims.
- Add local HTTP/1.1 and HTTP/2 identity tests, redirect/header-order checks, deterministic-selection fixtures and configuration-isolation regression tests.

### Compatibility

- Cooldown is off by default. Enabled cooldowns survive cookie clearing and proxy/transport changes; invalid configurations remain errors after attempted transport recovery. Existing requests continue, and capacity limits can evict the earliest active entry.
- URL-scoped cookie readers exclude manual request overrides and include HttpOnly cookies for HTTP use. They do not export original Set-Cookie attributes or preserve optional source quoting. Cookie helpers now reject credential-bearing URLs, non-HTTP schemes and invalid ports.
- Existing defaults and TLS transport dependencies remain unchanged. Add `github.com/dunglas/httpsfv v1.1.2` for structured-field parsing. New identity settings configure HTTP metadata and do not replace the selected TLS/HTTP2 preset.
- Exit-IP lookup is disabled by default and constructors stay offline. Automatic lookup shares the business request's total deadline; prewarm before latency-sensitive requests. Timezone metadata does not create a JS environment; country-language defaults do not identify device/browser type. Go's embedded tzdata adds portable timezone validation without a new module dependency.
- Eager high-entropy and negotiated hints are separate opt-in modes and mutually exclusive. Negotiation requires HTTPS and complete caller-supplied Chromium identity metadata; construction stays offline.
- Critical retries are per current URL, bounded to one per call, and retain the original total deadline. Document snapshots do not inherit prior document preferences. Full browser navigation restarts, DOM/iframe policy inheritance, Clear-Site-Data, JavaScript/device environments, modern Safari/Edge/Android browser presets remain outside this change.
- The previously documented upstream HTTP/2 blocked-upload limitation is fixed in the bundled transport; blocking readers must still support concurrent Close that unblocks Read.

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
