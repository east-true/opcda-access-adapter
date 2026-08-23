package opcda

import "time"

const (
	rpcEConnectionTerminated HRESULT = -2147418106 // 0x80010006
	rpcEServerDied           HRESULT = -2147418105 // 0x80010007
	rpcEServerDiedDNE        HRESULT = -2147418094 // 0x80010012
	rpcEDisconnected         HRESULT = -2147417848 // 0x80010108
	coEObjectNotConnected    HRESULT = -2147220995 // 0x800401FD
	rpcSServerUnavailable    HRESULT = -2147023174 // 0x800706BA
	rpcSCallFailed           HRESULT = -2147023170 // 0x800706BE
	rpcSCallFailedDNE        HRESULT = -2147023169 // 0x800706BF
)

func isConnectionLossHRESULT(hr HRESULT) bool {
	switch hr {
	case rpcEConnectionTerminated, rpcEServerDied, rpcEServerDiedDNE,
		rpcEDisconnected, coEObjectNotConnected, rpcSServerUnavailable,
		rpcSCallFailed, rpcSCallFailedDNE:
		return true
	default:
		return false
	}
}

func isConnectionLoss(err error) bool {
	sourceError, ok := AsSourceError(err)
	return ok && isConnectionLossHRESULT(sourceError.HRESULT)
}

// reconnectDelay returns capped exponential backoff with 80-120% jitter.
// jitterValue is injected so the bounds and progression remain deterministic
// in tests; callers obtain it from bounded runtime state.
func reconnectDelay(attempt uint32, initial, maximum time.Duration, jitterValue uint64) time.Duration {
	base := initial
	for count := uint32(0); count < attempt && base < maximum; count++ {
		if base > maximum/2 {
			base = maximum
		} else {
			base *= 2
		}
	}
	if base > maximum {
		base = maximum
	}
	permille := int64(800 + jitterValue%401)
	var delay time.Duration
	if permille <= 1000 {
		delay = time.Duration(int64(base) / 1000 * permille)
	} else {
		extra := time.Duration(int64(base) / 1000 * (permille - 1000))
		if base >= maximum-extra {
			return maximum
		}
		delay = base + extra
	}
	if delay <= 0 {
		delay = initial
	}
	if delay > maximum {
		return maximum
	}
	return delay
}
