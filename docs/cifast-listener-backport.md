# CIFAST scale-set listener transport backport

This branch starts at GARM v0.2.1 commit
`154638445c3949c1958b01812f69d9a1e4d82684`. It selectively carries the
scale-set request deadlines and HTTP/2 health checks from upstream main
`33ec0921418a470ca7e00dfb20ae111595da24e1`. It leaves the generic
`ForgeCredentials.GetHTTPClient` implementation unchanged.

Both scale-set clients share one unauthenticated transport. Ordinary calls
have a 60-second client timeout; `GetMessage` uses 100 seconds. For HTTP/2,
Go 1.26.2 is configured to send a health ping after 30 seconds without frames
and close the connection if the ping is not acknowledged within 15 seconds.
These health settings do not establish that the connection negotiated HTTP/2.
Each scale-set request retains its own authorization header. The transport
uses the configured CA bundle, proxy, TLS minimum, and dial settings; a
malformed configured CA bundle refuses construction.

The change adds no application retry for a timed-out mutation. A timeout does
not prove whether the server completed the operation. Existing session and
message behavior remains subject to that ambiguity.

The hosted CI job checks the pinned Go version, runs source-bound failure
controls, builds the embedded UI from the committed lockfile, and uploads the
binary, source SHA, run ID, Go build information and binary SHA-256. The
failure controls use a URL from this release's README and an injected failing
transport; they do not contact GitHub or invent a successful response. A real
scale-set authorization header was not available as a public, non-secret
fixture, so runtime authorization-header preservation remains unobserved; the
unchanged request-header path is subject to source review. CI configuration
alone is not a passing run or deployment authorization.

The installed controller binary, unit behavior, artifact compatibility,
quiescence and the active-runner join must be independently observed before a
seat-authorized window. The acceptance window requires two listener sessions,
natural slot release and 48 hours of passive observation; those outcomes are
not established by a source change or CI artifact.
