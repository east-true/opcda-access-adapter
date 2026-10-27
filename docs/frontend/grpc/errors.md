# Batch and error semantics

[Frontend overview](README.md) · [Documentation](../../README.md)

Read and Write are batch-first and preserve request order. A source result such
as success/failure/success returns a successful gRPC response with three
ordered result messages and each item's HRESULT/error. Bad Quality is data,
not a transport failure.

Request-level failures use canonical gRPC codes and attach a typed
`DAOperationError` detail:

Every adapter error code the gRPC frontend can return, and the canonical status
it arrives as. The codes themselves are described in the
[HTTP reference](../http/errors.md#errors); this is the mapping, not a second
vocabulary.

| gRPC status | Adapter error codes |
|---|---|
| `InvalidArgument` | `INVALID_REQUEST`, `INVALID_VALUE`, `ITEM_ID_TOO_LONG`, `BSTR_TOO_LONG`, `TYPE_MISMATCH` |
| `ResourceExhausted` | `REQUEST_LIMIT_EXCEEDED`, `BROWSE_RESULT_LIMIT_EXCEEDED`, `REGISTERED_ITEM_LIMIT_EXCEEDED`, `QUEUE_FULL`, `SUBSCRIPTION_LIMIT_EXCEEDED` |
| `Unimplemented` | `BROWSE_UNSUPPORTED`, `PROPERTIES_UNSUPPORTED`, `SUBSCRIBE_UNSUPPORTED`, `UNSUPPORTED_VARTYPE` |
| `PermissionDenied` | `WRITE_DISABLED` |
| `DeadlineExceeded` | `RUNTIME_DEADLINE_EXCEEDED` |
| `NotFound` | `SUBSCRIPTION_NOT_FOUND` |
| `Aborted` | `SUBSCRIPTION_INVALIDATED` |
| `Internal` | `INTERNAL_RESULT_MISMATCH` |
| `Unavailable` | `RUNTIME_UNAVAILABLE`, `DA_METHOD_FAILED`, and anything not named above |

`Unavailable` is the default, so an unmapped code arrives as `Unavailable`
rather than as something more specific. A failure that is neither a source
error nor an adapter error is the one case with no adapter code behind it: it
arrives as `Internal` carrying `INTERNAL_ERROR`. `Aborted` is deliberate for
`SUBSCRIPTION_INVALIDATED`: the client must resubscribe explicitly, and the
call must not look transparently retryable. A cancelled call arrives as
`Canceled` carrying `RUNTIME_DEADLINE_EXCEEDED`.

A `DA_METHOD_FAILED` detail carries the operation name and the raw HRESULT; the
frontend and adapter layers carry the code and a bounded message.

| Layer | Preserved detail |
|---|---|
| frontend/validation | adapter error code and bounded message |
| adapter/runtime | adapter error code |
| DA source method | operation and raw HRESULT |

The adapter does not configure automatic retries. In particular, a Write whose
client deadline expires has an unknown source outcome and must not be replayed
automatically.
