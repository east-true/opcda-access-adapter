# ADR-0009: request parser and lifecycle hardening

- Status: Accepted
- Date: 2026-08-24

## Context

The bounded v0 HTTP body still passed through Go's default JSON field matching.
That decoder accepts duplicate object keys by keeping the last value and
matches struct fields without requiring the documented letter case. Deeply
nested input was bounded in bytes but not in parser stack depth. Decoder error
text could also reflect an attacker-controlled unknown field into the response.

HTTP routing accepted query strings and some percent-encoded spellings of an
otherwise known path, and returned 404 rather than an explicit method contract.
Those behaviors create avoidable disagreement with reverse proxies and
security controls. The adapter has no compressed request-body contract.

At the opposite boundary, the Windows runtime normally produces exact scalar
types and Browse identities, but the HTTP layer did not fail closed if an
internal defect violated that contract. An asynchronous HTTP listener failure
was also discarded, leaving the process alive while status was unreachable.

## Decision

- Reject duplicate JSON object keys after unescaping.
- Reject case aliases of every documented request field.
- Bound JSON container nesting to configurable `OPCDA_MAX_JSON_DEPTH`, default
  64 and hard ceiling 256.
- Return stable parser errors that do not reflect decoder-controlled input.
- Require exact, unencoded v0 paths with no query component. Known paths return
  405 plus `Allow` for a wrong method, and Status rejects a request body.
- Reject every valid-method Read, Browse, and Status request carrying an
  `Origin` header, including an empty value. An enabled Write applies the same
  rule; disabled Write still fails first with `WRITE_DISABLED`. POST requests
  require exactly one JSON Content-Type and no Content-Encoding.
- Whitelist the exact Go scalar width for each source VARTYPE before JSON
  encoding. Validate successful Read metadata, Write outcome presence, Browse
  path correspondence, entry kind, and exact item identity before responding.
- Treat listener bind failure as terminal and shut down the already-created DA
  runtime with a bounded cleanup timeout. Report an unexpected asynchronous
  Serve failure to the application so it can shut down and exit non-zero.

## Consequences

Previously tolerated ambiguous requests now fail explicitly. Clients must use
the documented field spelling and cannot attach query parameters, encoded path
aliases, compressed bodies, or bodies to Status. These are not supported v0
features, so the stricter behavior does not change the product boundary.

Malformed source/runtime representations are never coerced into a successful
HTTP result. Unsupported or width-mismatched scalar values remain explicit
item errors where partial-batch semantics can be preserved; broken identity or
outcome structure fails the operation with `INTERNAL_RESULT_MISMATCH`.

The new checks are transport and lifecycle defenses only. They do not add
authentication, persistence, source transformation, remote DCOM, or a new
frontend.
