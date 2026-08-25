package opcua

import (
	"math"
	"time"
)

// OPC 10000-6 5.2.2.5 defines DateTime as a signed 64-bit count of 100
// nanosecond intervals since 1601-01-01 00:00:00 UTC, with explicit rules for
// values outside a platform's range.
const (
	dateTimeTicksPerSecond = int64(10_000_000)
	// Seconds between 1601-01-01 and the Unix epoch.
	dateTimeEpochOffsetSeconds = int64(11_644_473_600)
)

// DateTimeMin and DateTimeMax are the encoded values the clause reserves for
// "earlier than 1601" and "later than 9999", which it states are invalid
// date/time values that applications should treat as such.
const (
	DateTimeMin int64 = 0
	DateTimeMax int64 = math.MaxInt64
)

// dateTimeUpperBound is 9999-12-31T23:59:59.999999900Z, the latest instant the
// clause represents before saturating to DateTimeMax.
var (
	dateTimeLowerBound = time.Date(1601, time.January, 1, 0, 0, 0, 0, time.UTC)
	dateTimeUpperBound = time.Date(9999, time.December, 31, 23, 59, 59, 999_999_900, time.UTC)
)

// EncodeDateTime converts an instant to the UA wire value, applying the
// saturation rules of OPC 10000-6 5.2.2.5 rather than wrapping.
//
// A zero time.Time is not the same as "no timestamp". Callers that must
// distinguish an absent source timestamp carry that presence bit separately,
// exactly as the DA core does.
func EncodeDateTime(value time.Time) int64 {
	utc := value.UTC()
	if !utc.After(dateTimeLowerBound) {
		return DateTimeMin
	}
	if !utc.Before(dateTimeUpperBound) {
		return DateTimeMax
	}
	seconds := utc.Unix() + dateTimeEpochOffsetSeconds
	// The bounds above keep this product well inside Int64.
	return seconds*dateTimeTicksPerSecond + int64(utc.Nanosecond())/100
}

// DecodeDateTime converts a UA wire value to an instant, applying the decoding
// rules of OPC 10000-6 5.2.2.5: the reserved minimum and maximum decode to the
// earliest and latest representable instants, and a negative value is earlier
// than the epoch and therefore decodes as the earliest.
func DecodeDateTime(ticks int64) time.Time {
	if ticks <= DateTimeMin {
		return dateTimeLowerBound
	}
	if ticks == DateTimeMax {
		return dateTimeUpperBound
	}
	seconds := ticks/dateTimeTicksPerSecond - dateTimeEpochOffsetSeconds
	nanoseconds := (ticks % dateTimeTicksPerSecond) * 100
	decoded := time.Unix(seconds, nanoseconds).UTC()
	if decoded.After(dateTimeUpperBound) {
		return dateTimeUpperBound
	}
	return decoded
}
