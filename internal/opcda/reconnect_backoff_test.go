package opcda

import (
	"testing"
	"time"
)

// reconnectDelay is capped exponential backoff with 80-120% jitter. The
// existing test walks the progression and checks the delay stays inside its
// bounds, which leaves three things unsaid: what the jitter range actually is
// at each end, that a delay can never come back as zero, and that the cap is
// reached rather than merely respected.
//
// A zero delay is the one that matters. The arithmetic truncates -- a base
// below a microsecond divides to nothing before the jitter is applied -- and a
// reconnect loop handed zero would spin against a source that is already down.
// The fallback exists for that, and the mutation removing it survived.
//
// Most of the other survivors in this function are equivalent mutants rather
// than gaps, and it is worth writing down which, so the next sweep does not
// spend the same time on them:
//
//   - `base > maximum/2` to `>=`: at exactly half, doubling lands on maximum,
//     which is what the other branch assigns. An odd maximum makes them differ
//     by a nanosecond, and the permille division truncates that away again.
//   - `base > maximum` and the loop's `base < maximum` to their inclusive
//     forms: the extra iteration assigns maximum to a base already equal to it.
//   - `base >= maximum-extra` to `>`: on that boundary base+extra is maximum
//     exactly, so both arms return the same duration by construction.
//   - `permille <= 1000` to `<`: at exactly 1000 the else branch computes a
//     zero extra and returns the same base.

func TestAReconnectDelayIsNeverZero(t *testing.T) {
	// A base this small divides to nothing: 1ns/1000 is 0, and 0 multiplied by
	// any jitter is still 0. Without the fallback the caller would be told to
	// retry immediately, forever.
	for jitter := uint64(0); jitter < 401; jitter++ {
		if got := reconnectDelay(0, time.Nanosecond, time.Second, jitter); got <= 0 {
			t.Fatalf("jitter %d produced a delay of %v, which would spin", jitter, got)
		}
	}
	// The same at a later attempt, where the base has been doubled but is still
	// under the divisor.
	if got := reconnectDelay(5, 2*time.Nanosecond, time.Second, 0); got <= 0 {
		t.Fatalf("a doubled sub-microsecond base produced %v", got)
	}
}

func TestReconnectJitterSpansEightyToOneHundredAndTwentyPercent(t *testing.T) {
	// A base well clear of the cap, so the jitter is what is being measured
	// rather than the saturation.
	const base = 10 * time.Second
	for _, testCase := range []struct {
		name   string
		jitter uint64
		want   time.Duration
	}{
		{"the lowest jitter is eighty percent", 0, 8 * time.Second},
		{"the middle jitter is the base itself", 200, base},
		{"the highest jitter is one hundred and twenty percent", 400, 12 * time.Second},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := reconnectDelay(0, base, time.Minute, testCase.jitter)
			if got != testCase.want {
				t.Errorf("jitter %d = %v, want %v", testCase.jitter, got, testCase.want)
			}
		})
	}

	// The jitter is drawn modulo 401, so a caller passing a larger number gets
	// a value inside the range rather than a delay outside it.
	if got := reconnectDelay(0, base, time.Minute, 401); got != 8*time.Second {
		t.Errorf("jitter 401 = %v, want the same as jitter 0", got)
	}
}

// The cap has to be reached, not merely respected: a backoff that grows towards
// a maximum it never attains would keep a source waiting longer each time.
func TestReconnectBackoffReachesItsCapAndStops(t *testing.T) {
	const initial, maximum = time.Second, 30 * time.Second
	// At the middle jitter the delay is the base, so this reads the base out.
	settled := reconnectDelay(20, initial, maximum, 200)
	if settled != maximum {
		t.Fatalf("after twenty attempts the delay is %v, want the cap %v", settled, maximum)
	}
	// And it stays there rather than growing past it.
	if later := reconnectDelay(40, initial, maximum, 200); later != maximum {
		t.Errorf("after forty attempts the delay is %v, want the cap %v", later, maximum)
	}
	// Even the upper jitter cannot push a capped delay past the maximum, which
	// is what the saturation branch is for.
	for jitter := uint64(0); jitter < 401; jitter++ {
		if got := reconnectDelay(20, initial, maximum, jitter); got > maximum {
			t.Fatalf("jitter %d pushed a capped delay to %v, past the cap %v", jitter, got, maximum)
		}
	}
}
