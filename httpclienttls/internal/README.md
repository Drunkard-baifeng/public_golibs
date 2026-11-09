# Patched transport sources

This directory carries a distributable fix for early HTTP/2 responses while
request-body reads are blocked. A module-cache edit or a local `go.mod replace`
would not propagate to applications importing httpclienttls. The pinned
tls-client API has no transport factory/setter through which this HTTP/2 fix
can be installed.

## Sources

- `http2`: non-test Go files in `github.com/bogdanfinn/fhttp/http2` at v0.6.8.
  The complete package is retained to keep upstream comparisons mechanical;
  its shared frame, connection and server helpers are not hand-extracted.
  The Go Authors' BSD license is included in `http2/LICENSE`.
- `tlsclient`: `client.go`, `client_options.go`, `connect.go`, `jar.go`,
  `logger.go`, `pinner.go`, `racer.go`, `roundtripper.go` and `utils.go` from
  `github.com/bogdanfinn/tls-client` v1.15.1, with its LICENSE retained.
  CLI, examples, JA3 helpers and WebSocket support are not copied.
- HTTP types, cookies, HPACK, browser profiles, bandwidth accounting, uTLS and
  HTTP/3 remain the pinned external dependencies in the module's go.mod.

This product includes software developed by Bogdan Finn and contributors.

## Maintained changes

`tlsclient/roundtripper.go` constructs the internal HTTP/2 transport and converts
the upstream profile's settings, settings order and priority types. TLS handshakes,
session caches and wire settings remain unchanged. Unclaimed negotiated
connections carry a cancellation callback, stopped on ownership transfer;
idle cleanup also closes these stashed connections.

`tlsclient/racer.go` restricts duplicate dispatch to bodyless GET/HEAD, clones
requests, uses independent caller-derived candidate contexts, cancels/closes
losers and keeps winner cancellation bound to body lifetime. It reuses the
actual cached HTTP/3 transport. A cached request failure is returned without
retrying a potentially already-processed operation. The 10-second selection
budget does not cancel a winning response stream.

`tlsclient/client.go` returns an error and cleans up bodies when its configured
synchronous panic catcher fires, rather than returning `(nil, nil)`.

`http2/transport.go`:

1. Marks upload cancellation before closing the body outside connection locks.
   Cancellation and writer cleanup share once-only closure.
2. Checks cancellation before reads and after both the main read and the
   Content-Length EOF probe, preserving early response bodies.
3. Joins the writer, including canceled Expect timers. Unknown-length uploads
   send END_STREAM only if not already sent. Incomplete known-length uploads
   wait for the response pipe to close, then release the remaining stream with
   NO_ERROR; they never declare a truncated upload complete.
4. Sends resets on request cancellation even when a delayed writer never ran,
   closes uploads on peer reset, and corrects the cancelStream reset guard.
5. Watches successful duplex responses until response completion, close,
   connection loss or cancellation. Joins the remaining body writer and cleans
   up unfinished streams even if writer completion wins the select. HEAD/no-body
   responses complete without waiting for an unread response stream.

Tests beside the snapshots cover delayed 100-continue and strict frame-level
known-length behavior. Package integration tests cover redirects, early error
statuses, streaming, once-only closure, cancellation, EOF probes and full duplex.
`TestPatchedTransportPreservesHTTP2Wire` compares actual initial frames against
upstream for Chrome, Firefox and Safari. Tests use local servers and controlled
transport fixtures; protocol-race unit tests do not claim live QUIC verification.

## Upgrades and analysis

Keep these snapshots synchronized deliberately when upgrading dependencies.
Compare against the recorded versions, reapply only the documented patches,
and run `go test -race ./...` plus vet/staticcheck. Remove the snapshots once an
upstream release fixes this issue and passes the same regressions. Merely
upgrading go.mod does not update the internal transport sources.

The scoped staticcheck configurations suppress only existing upstream snapshot
diagnostics (legacy APIs, style, unused internal helpers and known allocation/
range checks). Other diagnostics, including concurrency checks, remain enabled.
Application code retains the default checks.
