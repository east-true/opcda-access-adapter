package opcua

import (
	"fmt"
	"time"
)

// SecureChannel token lifecycle, from OPC 10000-6 6.7.4 and OPC 10000-4 7.36.
//
// The rules this implements hold even with SecurityMode None, which 6.7.4
// states explicitly: the SecureChannelId and TokenId are still assigned, the
// token shall still be renewed before its RevisedLifetime expires, and
// receivers shall still ignore invalid or expired TokenIds.

// Secure channel status codes, from the OPC Foundation StatusCode list.
const (
	StatusBadSecureChannelIDInvalid    StatusCode = 0x80220000
	StatusBadSecureChannelClosed       StatusCode = 0x80860000
	StatusBadSecureChannelTokenUnknown StatusCode = 0x80870000
)

// TokenRequestType is SecurityTokenRequestType. OPC 10000-4 describes Issue as
// creating a token for a new channel and Renew as creating one for an existing
// channel. The wire values are taken from the OPC Foundation UA NodeSet, whose
// DataType definition for SecurityTokenRequestType gives Issue 0 and Renew 1.
type TokenRequestType int32

const (
	TokenRequestIssue TokenRequestType = 0
	TokenRequestRenew TokenRequestType = 1
)

func (t TokenRequestType) String() string {
	switch t {
	case TokenRequestIssue:
		return "Issue"
	case TokenRequestRenew:
		return "Renew"
	default:
		return fmt.Sprintf("Unknown(%d)", int(t))
	}
}

// ChannelSecurityToken is the token of OPC 10000-6 Table 64. RevisedLifetime is
// a UInt32 count of milliseconds rather than the abstract Duration of
// OPC 10000-4, which 6.7.4 states is a deliberate optimisation.
type ChannelSecurityToken struct {
	SecureChannelID uint32
	TokenID         uint32
	CreatedAt       time.Time
	RevisedLifetime uint32
}

// ExpiresAt is CreatedAt plus the lifetime, as OPC 10000-4 7.36 defines it.
func (t ChannelSecurityToken) ExpiresAt() time.Time {
	return t.CreatedAt.Add(time.Duration(t.RevisedLifetime) * time.Millisecond)
}

func (t ChannelSecurityToken) expired(now time.Time) bool {
	return !now.Before(t.ExpiresAt())
}

// ChannelLimits bounds what a peer can ask a server to hold open.
type ChannelLimits struct {
	// MinLifetime and MaxLifetime bound the revised token lifetime.
	// OPC 10000-4 requires the server to provide a lifetime greater than zero.
	MinLifetime time.Duration
	MaxLifetime time.Duration
	MaxChannels int
}

func DefaultChannelLimits() ChannelLimits {
	return ChannelLimits{
		MinLifetime: 10 * time.Second,
		MaxLifetime: 10 * time.Minute,
		MaxChannels: 16,
	}
}

func (limits ChannelLimits) validate() error {
	if limits.MinLifetime <= 0 || limits.MaxLifetime <= 0 || limits.MaxChannels <= 0 {
		return fmt.Errorf("all secure channel limits must be positive")
	}
	if limits.MinLifetime > limits.MaxLifetime {
		return fmt.Errorf("minimum token lifetime must not exceed the maximum")
	}
	// The lifetime is carried as a UInt32 of milliseconds.
	if limits.MaxLifetime/time.Millisecond > time.Duration(^uint32(0)) {
		return fmt.Errorf("maximum token lifetime must fit in a UInt32 of milliseconds")
	}
	return nil
}

func (limits ChannelLimits) ValidateForConfiguration() error { return limits.validate() }

// reviseLifetime clamps a client's request into the configured range. The
// result is always greater than zero, which OPC 10000-4 requires of a server.
func (limits ChannelLimits) reviseLifetime(requested uint32) uint32 {
	requestedDuration := time.Duration(requested) * time.Millisecond
	switch {
	case requestedDuration < limits.MinLifetime:
		requestedDuration = limits.MinLifetime
	case requestedDuration > limits.MaxLifetime:
		requestedDuration = limits.MaxLifetime
	}
	return uint32(requestedDuration / time.Millisecond)
}

// SecureChannel holds one channel's token state.
//
// A renewal does not invalidate the previous token at once. OPC 10000-6 6.7.4
// requires the server to keep accepting the old token until it expires or until
// a message secured with the new token arrives, so a client that is still
// finishing the renewal is not cut off.
type SecureChannel struct {
	id           uint32
	securityMode SecurityMode
	limits       ChannelLimits

	current  ChannelSecurityToken
	previous *ChannelSecurityToken
	closed   bool
}

func (c *SecureChannel) ID() uint32                 { return c.id }
func (c *SecureChannel) SecurityMode() SecurityMode { return c.securityMode }
func (c *SecureChannel) Token() ChannelSecurityToken {
	return c.current
}

// PreviousToken reports the superseded token while it is still acceptable.
func (c *SecureChannel) PreviousToken() (ChannelSecurityToken, bool) {
	if c.previous == nil {
		return ChannelSecurityToken{}, false
	}
	return *c.previous, true
}

// Renew issues a new token for the same channel and keeps the old one
// acceptable until it expires or is superseded by use of the new one.
func (c *SecureChannel) Renew(tokenID uint32, requestedLifetime uint32, now time.Time) (ChannelSecurityToken, error) {
	if c.closed {
		return ChannelSecurityToken{}, uacpError(StatusBadSecureChannelClosed,
			"secure channel %d is closed", c.id)
	}
	superseded := c.current
	c.previous = &superseded
	c.current = ChannelSecurityToken{
		SecureChannelID: c.id,
		TokenID:         tokenID,
		CreatedAt:       now,
		RevisedLifetime: c.limits.reviseLifetime(requestedLifetime),
	}
	return c.current, nil
}

// AcceptToken validates the TokenId on an incoming chunk. An unknown or expired
// token is refused, which 6.7.4 requires even when no security is applied.
// Accepting the new token retires the previous one, as the clause describes.
func (c *SecureChannel) AcceptToken(tokenID uint32, now time.Time) error {
	if c.closed {
		return uacpError(StatusBadSecureChannelClosed, "secure channel %d is closed", c.id)
	}
	if tokenID == c.current.TokenID {
		if c.current.expired(now) {
			return uacpError(StatusBadSecureChannelTokenUnknown,
				"token %d expired at %s", tokenID, c.current.ExpiresAt().UTC().Format(time.RFC3339Nano))
		}
		// The client has moved to the new token, so the old one is retired.
		c.previous = nil
		return nil
	}
	if c.previous != nil && tokenID == c.previous.TokenID {
		if c.previous.expired(now) {
			c.previous = nil
			return uacpError(StatusBadSecureChannelTokenUnknown,
				"superseded token %d has expired", tokenID)
		}
		return nil
	}
	return uacpError(StatusBadSecureChannelTokenUnknown, "token %d is not known to channel %d", tokenID, c.id)
}

// Expired reports whether every token this channel holds has expired, which is
// when a server may reclaim it.
func (c *SecureChannel) Expired(now time.Time) bool {
	if c.previous != nil && !c.previous.expired(now) {
		return false
	}
	return c.current.expired(now)
}

func (c *SecureChannel) Close()       { c.closed = true }
func (c *SecureChannel) Closed() bool { return c.closed }

// ChannelRegistry issues and tracks secure channels. It is not safe for
// concurrent use; a server owns one per listener and drives it from that
// listener's goroutine.
type ChannelRegistry struct {
	limits   ChannelLimits
	channels map[uint32]*SecureChannel

	nextChannelID uint32
	nextTokenID   uint32
}

// NewChannelRegistry seeds the identifier counters. OPC 10000-6 Table 57
// advises that the first SecureChannelId after a restart should be unlikely to
// collide with one a previously connected client still holds, so the caller
// supplies the seed rather than every server starting at the same value.
func NewChannelRegistry(limits ChannelLimits, channelIDSeed, tokenIDSeed uint32) (*ChannelRegistry, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	return &ChannelRegistry{
		limits:        limits,
		channels:      make(map[uint32]*SecureChannel),
		nextChannelID: channelIDSeed,
		nextTokenID:   tokenIDSeed,
	}, nil
}

// Zero is not a valid identifier for either a channel or a token, so the
// counters skip it on wrap-around.
func (r *ChannelRegistry) allocateChannelID() uint32 {
	for {
		r.nextChannelID++
		if r.nextChannelID == 0 {
			continue
		}
		if _, taken := r.channels[r.nextChannelID]; !taken {
			return r.nextChannelID
		}
	}
}

func (r *ChannelRegistry) allocateTokenID() uint32 {
	r.nextTokenID++
	if r.nextTokenID == 0 {
		r.nextTokenID = 1
	}
	return r.nextTokenID
}

// Issue opens a channel. The security mode is checked first, so a mode the
// adapter cannot provide never results in an open channel.
func (r *ChannelRegistry) Issue(mode SecurityMode, requestedLifetime uint32, now time.Time) (*SecureChannel, error) {
	if err := RequireSupportedSecurityMode(mode); err != nil {
		return nil, err
	}
	if len(r.channels) >= r.limits.MaxChannels {
		return nil, uacpError(StatusBadTcpNotEnoughResources,
			"the %d secure channel limit is reached", r.limits.MaxChannels)
	}
	channel := &SecureChannel{
		id:           r.allocateChannelID(),
		securityMode: mode,
		limits:       r.limits,
	}
	channel.current = ChannelSecurityToken{
		SecureChannelID: channel.id,
		TokenID:         r.allocateTokenID(),
		CreatedAt:       now,
		RevisedLifetime: r.limits.reviseLifetime(requestedLifetime),
	}
	r.channels[channel.id] = channel
	return channel, nil
}

// Renew issues a new token for an existing channel.
func (r *ChannelRegistry) Renew(channelID uint32, requestedLifetime uint32, now time.Time) (ChannelSecurityToken, error) {
	channel, err := r.Lookup(channelID)
	if err != nil {
		return ChannelSecurityToken{}, err
	}
	return channel.Renew(r.allocateTokenID(), requestedLifetime, now)
}

// Lookup resolves a channel. OPC 10000-6 Table 57 requires an unrecognised
// SecureChannelId to be reported as a transport error.
func (r *ChannelRegistry) Lookup(channelID uint32) (*SecureChannel, error) {
	channel, ok := r.channels[channelID]
	if !ok {
		return nil, uacpError(StatusBadTcpSecureChannelUnknown, "secure channel %d is not known", channelID)
	}
	if channel.closed {
		return nil, uacpError(StatusBadSecureChannelClosed, "secure channel %d is closed", channelID)
	}
	return channel, nil
}

// Accept resolves a channel and validates the token in one step, which is what
// a receiver does for every incoming chunk.
func (r *ChannelRegistry) Accept(channelID, tokenID uint32, now time.Time) (*SecureChannel, error) {
	channel, err := r.Lookup(channelID)
	if err != nil {
		return nil, err
	}
	if err := channel.AcceptToken(tokenID, now); err != nil {
		return nil, err
	}
	return channel, nil
}

// Close removes a channel, which is what a CloseSecureChannel request does.
func (r *ChannelRegistry) Close(channelID uint32) error {
	channel, ok := r.channels[channelID]
	if !ok {
		return uacpError(StatusBadTcpSecureChannelUnknown, "secure channel %d is not known", channelID)
	}
	channel.Close()
	delete(r.channels, channelID)
	return nil
}

// ExpireStale removes channels whose tokens have all expired and reports how
// many were reclaimed, so a peer cannot hold slots by going silent.
func (r *ChannelRegistry) ExpireStale(now time.Time) int {
	removed := 0
	for id, channel := range r.channels {
		if channel.Expired(now) {
			channel.Close()
			delete(r.channels, id)
			removed++
		}
	}
	return removed
}

func (r *ChannelRegistry) Count() int { return len(r.channels) }

// RequireProtocolVersion implements OPC 10000-6 6.7.4: the version in the
// OpenSecureChannel request shall match the one from the Hello, and a mismatch
// closes the channel with Bad_ProtocolVersionUnsupported.
func RequireProtocolVersion(helloVersion, requestVersion uint32) error {
	if helloVersion == requestVersion {
		return nil
	}
	return uacpError(StatusBadProtocolVersionUnsupport,
		"OpenSecureChannel version %d does not match the Hello version %d", requestVersion, helloVersion)
}
