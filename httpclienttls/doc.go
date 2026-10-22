// Package httpclienttls provides a reusable HTTP client with coherent browser
// TLS, HTTP/2, User-Agent, Client Hints, compression, and header-order profiles.
// A profile is selected once per Client and remains stable for its lifetime.
//
// Do buffers a response subject to Config.MaxResponseBodyBytes. DoStream and
// PostMultipartStream return a live body that callers must close. Long-lived
// streams can opt out of the client's total timeout with Options.DisableTimeout
// while retaining context cancellation, HeaderTimeout, and ReadIdleTimeout.
package httpclienttls
