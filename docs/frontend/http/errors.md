# Errors

[Frontend overview](README.md) · [Documentation](../../README.md)

Error bodies distinguish `frontend`, `adapter`, and `source` layers. Source
method errors include the raw HRESULT. Item errors are not replaced by a
generic request error.

Every code the adapter can return is below. The `frontend` layer rejects a
request before any source work; the `adapter` layer is the runtime refusing or
failing; a `source` error is the vendor's, and carries its raw HRESULT.

| Code | HTTP | Layer | Means |
|---|---:|---|---|
| `INVALID_REQUEST` | 400 | frontend/adapter | the request is malformed or asks for something this endpoint does not answer |
| `INVALID_VALUE` | 400 | adapter | a Write value does not fit the VARTYPE it was given |
| `ITEM_ID_TOO_LONG` | 400 | adapter | an ItemID exceeds `OPCDA_MAX_ITEM_ID_BYTES` |
| `BSTR_TOO_LONG` | 400 | adapter | a string exceeds `OPCDA_MAX_BSTR_CODE_UNITS` |
| `ARRAY_TOO_LARGE` | 422 | adapter | an array exceeds `OPCDA_MAX_ARRAY_ELEMENTS` or `OPCDA_MAX_ARRAY_DIMENSIONS` |
| `REQUEST_LIMIT_EXCEEDED` | 400 | adapter | a batch exceeds its per-request bound. A properties response carrying more properties than the bound is the one place this is 422: the request was fine and the source overran |
| `JSON_DEPTH_LIMIT_EXCEEDED` | 400 | frontend | nesting exceeds `OPCDA_MAX_JSON_DEPTH` |
| `DUPLICATE_JSON_FIELD` | 400 | frontend | an object repeats a field, which is ambiguous |
| `METHOD_NOT_ALLOWED` | 405 | frontend | known endpoint, wrong method; the response carries `Allow` |
| `UNSUPPORTED_MEDIA_TYPE` | 415 | frontend | the body is not `application/json` |
| `UNSUPPORTED_CONTENT_ENCODING` | 415 | frontend | a compressed body, which is not accepted |
| `REQUEST_BODY_TOO_LARGE` | 413 | frontend | the body exceeds `OPCDA_MAX_HTTP_BODY_BYTES` |
| `BROWSER_ORIGIN_REJECTED` | 403 | frontend | the request carried an `Origin` header |
| `UNTRUSTED_HOST` | 421 | frontend | a loopback listener received a non-loopback `Host` |
| `WRITE_DISABLED` | 403 | adapter | Write was not explicitly enabled |
| `BROWSE_UNSUPPORTED` | 422 | adapter | the source does not implement `IOPCBrowseServerAddressSpace` |
| `PROPERTIES_UNSUPPORTED` | 422 | adapter | the source does not implement `IOPCItemProperties` |
| `SUBSCRIBE_UNSUPPORTED` | 503 | adapter | the source offers no `IOPCDataCallback` connection point |
| `BROWSE_RESULT_LIMIT_EXCEEDED` | 422 | adapter | a Browse produced more than `OPCDA_MAX_BROWSE_ENTRIES` |
| `DETECTION_RESULT_LIMIT_EXCEEDED` | 503 | adapter | local detection found more registrations than its bound. Detection is a CLI command, so this does not arise over HTTP |
| `UNSUPPORTED_VARTYPE` | 422 | adapter | the value's VARTYPE has no lossless representation here |
| `TYPE_MISMATCH` | 503 | adapter | a Write named a VARTYPE the item does not accept |
| `REGISTERED_ITEM_LIMIT_EXCEEDED` | 503 | adapter | the item-registration cache is full for this connection generation |
| `QUEUE_FULL` | 503 | adapter | the serialized DA command queue is at `OPCDA_COMMAND_QUEUE` |
| `RUNTIME_UNAVAILABLE` | 503 | adapter | no usable source connection: disconnected, reconnecting, or degraded |
| `SUBSCRIPTION_NOT_FOUND` | 503 | adapter | the subscription is unknown, or belongs to an ended generation |
| `SUBSCRIPTION_LIMIT_EXCEEDED` | 503 | adapter | `OPCDA_MAX_SUBSCRIPTIONS` concurrent subscriptions already exist |
| `SUBSCRIPTION_INVALIDATED` | 503 | adapter | the connection generation ended under an open subscription |
| `RUNTIME_DEADLINE_EXCEEDED` | 504 | adapter | the operation outlived `OPCDA_REQUEST_DEADLINE` |
| `INTERNAL_RESULT_MISMATCH` | 500 | adapter | a Read/Write result count or ordered ItemID did not match the request, and the response fails closed |
| `INTERNAL_ERROR` | 500 | adapter | an adapter failure that fits no other code |
| `NOT_FOUND` | 404 | frontend | the request target is not one of the endpoints above |
| `DA_METHOD_FAILED` | 503 | source | the DA method failed; the body carries the vendor's raw HRESULT and the operation name |

`SUBSCRIPTION_*` and `SUBSCRIBE_UNSUPPORTED` are reachable over gRPC and OPC UA;
HTTP exposes no Subscribe. The gRPC frontend maps these same codes onto
canonical gRPC statuses, described in the
[gRPC API reference](../grpc/errors.md#batch-and-error-semantics).
