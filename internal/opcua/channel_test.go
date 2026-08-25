package opcua

import (
	"testing"
	"time"
)

var channelEpoch = time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)

func newTestRegistry(t *testing.T, limits ChannelLimits) *ChannelRegistry {
	t.Helper()
	registry, err := NewChannelRegistry(limits, 1000, 2000)
	if err != nil {
		t.Fatalf("NewChannelRegistry: %v", err)
	}
	return registry
}

// OPC 10000-4 Table 139: Invalid is 0 so an unset field is never mistaken for a
// deliberate choice of no security.
func TestSecurityModeWireValues(t *testing.T) {
	cases := map[SecurityMode]uint32{
		SecurityModeInvalid: 0, SecurityModeNone: 1,
		SecurityModeSign: 2, SecurityModeSignAndEncrypt: 3,
	}
	for mode, want := range cases {
		if uint32(mode) != want {
			t.Fatalf("%s = %d, want %d", mode, uint32(mode), want)
		}
	}
	// Table 139 says Invalid "will always be rejected".
	if err := RequireSupportedSecurityMode(SecurityModeInvalid); err == nil {
		t.Fatal("the Invalid security mode was accepted")
	}
}

// OPC 10000-6 6.7.4: the channel and token ids are assigned even with no
// security applied, and the lifetime is still revised and enforced.
func TestIssueAssignsIdentifiersEvenWithoutSecurity(t *testing.T) {
	registry := newTestRegistry(t, DefaultChannelLimits())
	channel, err := registry.Issue(SecurityModeNone, 60_000, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if channel.ID() == 0 || channel.Token().TokenID == 0 {
		t.Fatalf("channel %d token %d: zero is not a valid identifier", channel.ID(), channel.Token().TokenID)
	}
	if channel.Token().SecureChannelID != channel.ID() {
		t.Fatal("the token does not carry its channel id")
	}
	if channel.Token().RevisedLifetime == 0 {
		t.Fatal("the server provided a zero lifetime")
	}
	if got := channel.Token().ExpiresAt(); !got.Equal(channelEpoch.Add(60 * time.Second)) {
		t.Fatalf("expiry = %s", got)
	}
}

func TestIssueRefusesUnsupportedSecurityModes(t *testing.T) {
	registry := newTestRegistry(t, DefaultChannelLimits())
	for _, mode := range []SecurityMode{SecurityModeSign, SecurityModeSignAndEncrypt, SecurityModeInvalid} {
		if _, err := registry.Issue(mode, 60_000, channelEpoch); err == nil {
			t.Fatalf("%s opened a channel", mode)
		}
	}
	// A refused request must not consume a slot.
	if registry.Count() != 0 {
		t.Fatalf("registry holds %d channels after refusals", registry.Count())
	}
}

// OPC 10000-4: the server shall provide a lifetime greater than zero, and it
// should be sensible for its configuration.
func TestLifetimeIsRevisedIntoRange(t *testing.T) {
	limits := ChannelLimits{MinLifetime: 10 * time.Second, MaxLifetime: time.Minute, MaxChannels: 4}
	registry := newTestRegistry(t, limits)

	tooShort, err := registry.Issue(SecurityModeNone, 0, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if tooShort.Token().RevisedLifetime != 10_000 {
		t.Fatalf("revised lifetime = %d, want the floor", tooShort.Token().RevisedLifetime)
	}

	tooLong, err := registry.Issue(SecurityModeNone, 3_600_000, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if tooLong.Token().RevisedLifetime != 60_000 {
		t.Fatalf("revised lifetime = %d, want the ceiling", tooLong.Token().RevisedLifetime)
	}

	inRange, err := registry.Issue(SecurityModeNone, 30_000, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if inRange.Token().RevisedLifetime != 30_000 {
		t.Fatalf("revised lifetime = %d, want the request honoured", inRange.Token().RevisedLifetime)
	}
}

// OPC 10000-6 6.7.4: the server keeps accepting the old token until it expires
// or a message secured with the new token arrives.
func TestRenewKeepsTheOldTokenAcceptableUntilTheNewOneIsUsed(t *testing.T) {
	registry := newTestRegistry(t, DefaultChannelLimits())
	channel, err := registry.Issue(SecurityModeNone, 60_000, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	oldToken := channel.Token()

	renewed, err := registry.Renew(channel.ID(), 60_000, channelEpoch.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if renewed.TokenID == oldToken.TokenID {
		t.Fatal("renewal reused the token id")
	}
	if renewed.SecureChannelID != channel.ID() {
		t.Fatal("renewal changed the channel id")
	}
	if _, ok := channel.PreviousToken(); !ok {
		t.Fatal("the superseded token was discarded immediately")
	}

	// The old token still works while it is unexpired.
	if err := channel.AcceptToken(oldToken.TokenID, channelEpoch.Add(31*time.Second)); err != nil {
		t.Fatalf("the old token was refused before expiry: %v", err)
	}
	// Using the new token retires the old one.
	if err := channel.AcceptToken(renewed.TokenID, channelEpoch.Add(32*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, ok := channel.PreviousToken(); ok {
		t.Fatal("the superseded token survived use of the new one")
	}
	if err := channel.AcceptToken(oldToken.TokenID, channelEpoch.Add(33*time.Second)); err == nil {
		t.Fatal("the retired token was still accepted")
	}
}

func TestSupersededTokenIsRefusedOnceItExpires(t *testing.T) {
	registry := newTestRegistry(t, DefaultChannelLimits())
	channel, err := registry.Issue(SecurityModeNone, 20_000, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	oldToken := channel.Token()
	if _, err := registry.Renew(channel.ID(), 60_000, channelEpoch.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	err = channel.AcceptToken(oldToken.TokenID, channelEpoch.Add(21*time.Second))
	if err == nil {
		t.Fatal("an expired superseded token was accepted")
	}
	if got := codecStatus(t, err); got != StatusBadSecureChannelTokenUnknown {
		t.Fatalf("status = %s, want Bad_SecureChannelTokenUnknown", got.Hex())
	}
}

// "Receivers shall still ignore invalid or expired TokenIds", even with no
// security applied.
func TestUnknownAndExpiredTokensAreRefused(t *testing.T) {
	registry := newTestRegistry(t, DefaultChannelLimits())
	channel, err := registry.Issue(SecurityModeNone, 20_000, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := channel.AcceptToken(channel.Token().TokenID+999, channelEpoch); err == nil {
		t.Fatal("an unknown token was accepted")
	}
	// Exactly at the expiry instant the token is already gone.
	err = channel.AcceptToken(channel.Token().TokenID, channel.Token().ExpiresAt())
	if err == nil {
		t.Fatal("a token was accepted at its expiry instant")
	}
	if got := codecStatus(t, err); got != StatusBadSecureChannelTokenUnknown {
		t.Fatalf("status = %s", got.Hex())
	}
}

func TestRegistryAcceptResolvesChannelAndToken(t *testing.T) {
	registry := newTestRegistry(t, DefaultChannelLimits())
	channel, err := registry.Issue(SecurityModeNone, 60_000, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Accept(channel.ID(), channel.Token().TokenID, channelEpoch); err != nil {
		t.Fatal(err)
	}
	// An unrecognised channel id is a transport error, per Table 57.
	_, err = registry.Accept(channel.ID()+999, channel.Token().TokenID, channelEpoch)
	if err == nil {
		t.Fatal("an unknown channel was accepted")
	}
	if got := codecStatus(t, err); got != StatusBadTcpSecureChannelUnknown {
		t.Fatalf("status = %s, want Bad_TcpSecureChannelUnknown", got.Hex())
	}
}

func TestCloseRemovesTheChannel(t *testing.T) {
	registry := newTestRegistry(t, DefaultChannelLimits())
	channel, err := registry.Issue(SecurityModeNone, 60_000, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Close(channel.ID()); err != nil {
		t.Fatal(err)
	}
	if registry.Count() != 0 {
		t.Fatalf("registry holds %d channels after close", registry.Count())
	}
	if !channel.Closed() {
		t.Fatal("the channel was not marked closed")
	}
	if err := channel.AcceptToken(channel.Token().TokenID, channelEpoch); err == nil {
		t.Fatal("a closed channel accepted a token")
	} else if got := codecStatus(t, err); got != StatusBadSecureChannelClosed {
		t.Fatalf("status = %s, want Bad_SecureChannelClosed", got.Hex())
	}
	if _, err := channel.Renew(1, 60_000, channelEpoch); err == nil {
		t.Fatal("a closed channel was renewed")
	}
	if err := registry.Close(channel.ID()); err == nil {
		t.Fatal("closing an unknown channel succeeded")
	}
}

// A peer must not be able to hold slots open by going silent.
func TestStaleChannelsAreReclaimed(t *testing.T) {
	registry := newTestRegistry(t, DefaultChannelLimits())
	short, err := registry.Issue(SecurityModeNone, 10_000, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	long, err := registry.Issue(SecurityModeNone, 600_000, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if removed := registry.ExpireStale(channelEpoch.Add(5 * time.Second)); removed != 0 {
		t.Fatalf("reclaimed %d live channels", removed)
	}
	if removed := registry.ExpireStale(channelEpoch.Add(11 * time.Second)); removed != 1 {
		t.Fatalf("reclaimed %d channels, want 1", removed)
	}
	if _, err := registry.Lookup(short.ID()); err == nil {
		t.Fatal("the expired channel is still resolvable")
	}
	if _, err := registry.Lookup(long.ID()); err != nil {
		t.Fatalf("the live channel was reclaimed: %v", err)
	}
}

// A channel is only stale once every token it holds has expired.
func TestAChannelWithALiveSupersededTokenIsNotStale(t *testing.T) {
	registry := newTestRegistry(t, DefaultChannelLimits())
	channel, err := registry.Issue(SecurityModeNone, 600_000, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	// Renew to a much shorter lifetime so the current token expires first.
	limitsShort := ChannelLimits{MinLifetime: time.Second, MaxLifetime: time.Minute, MaxChannels: 4}
	channel.limits = limitsShort
	if _, err := channel.Renew(99, 1000, channelEpoch); err != nil {
		t.Fatal(err)
	}
	if channel.Expired(channelEpoch.Add(2 * time.Second)) {
		t.Fatal("a channel with a live superseded token was reported stale")
	}
	if !channel.Expired(channelEpoch.Add(601 * time.Second)) {
		t.Fatal("a channel whose tokens have all expired was reported live")
	}
}

func TestRegistryBoundsConcurrentChannels(t *testing.T) {
	limits := DefaultChannelLimits()
	limits.MaxChannels = 2
	registry := newTestRegistry(t, limits)
	for count := 0; count < limits.MaxChannels; count++ {
		if _, err := registry.Issue(SecurityModeNone, 60_000, channelEpoch); err != nil {
			t.Fatal(err)
		}
	}
	_, err := registry.Issue(SecurityModeNone, 60_000, channelEpoch)
	if err == nil {
		t.Fatal("the channel limit was exceeded")
	}
	if got := codecStatus(t, err); got != StatusBadTcpNotEnoughResources {
		t.Fatalf("status = %s, want Bad_TcpNotEnoughResources", got.Hex())
	}
}

func TestIdentifiersAreUniqueAndNeverZero(t *testing.T) {
	limits := DefaultChannelLimits()
	limits.MaxChannels = 64
	registry, err := NewChannelRegistry(limits, ^uint32(0)-2, ^uint32(0)-2)
	if err != nil {
		t.Fatal(err)
	}
	seenChannels := make(map[uint32]struct{})
	seenTokens := make(map[uint32]struct{})
	for count := 0; count < 16; count++ {
		channel, issueErr := registry.Issue(SecurityModeNone, 60_000, channelEpoch)
		if issueErr != nil {
			t.Fatal(issueErr)
		}
		if channel.ID() == 0 || channel.Token().TokenID == 0 {
			t.Fatal("an identifier wrapped to zero")
		}
		if _, duplicate := seenChannels[channel.ID()]; duplicate {
			t.Fatalf("channel id %d was reused", channel.ID())
		}
		if _, duplicate := seenTokens[channel.Token().TokenID]; duplicate {
			t.Fatalf("token id %d was reused", channel.Token().TokenID)
		}
		seenChannels[channel.ID()] = struct{}{}
		seenTokens[channel.Token().TokenID] = struct{}{}
	}
}

// OPC 10000-6 6.7.4: the OpenSecureChannel version shall match the Hello.
func TestProtocolVersionMustMatchTheHello(t *testing.T) {
	if err := RequireProtocolVersion(ProtocolVersion, ProtocolVersion); err != nil {
		t.Fatal(err)
	}
	err := RequireProtocolVersion(ProtocolVersion, ProtocolVersion+1)
	if err == nil {
		t.Fatal("a mismatched protocol version was accepted")
	}
	if got := codecStatus(t, err); got != StatusBadProtocolVersionUnsupport {
		t.Fatalf("status = %s, want Bad_ProtocolVersionUnsupported", got.Hex())
	}
}

func TestChannelLimitsValidation(t *testing.T) {
	if err := DefaultChannelLimits().ValidateForConfiguration(); err != nil {
		t.Fatalf("default limits rejected: %v", err)
	}
	for name, mutate := range map[string]func(*ChannelLimits){
		"zero minimum":    func(l *ChannelLimits) { l.MinLifetime = 0 },
		"zero maximum":    func(l *ChannelLimits) { l.MaxLifetime = 0 },
		"zero channels":   func(l *ChannelLimits) { l.MaxChannels = 0 },
		"inverted range":  func(l *ChannelLimits) { l.MinLifetime = l.MaxLifetime + time.Second },
		"beyond a UInt32": func(l *ChannelLimits) { l.MaxLifetime = time.Duration(^uint32(0))*time.Millisecond + time.Millisecond },
	} {
		t.Run(name, func(t *testing.T) {
			limits := DefaultChannelLimits()
			mutate(&limits)
			if err := limits.ValidateForConfiguration(); err == nil {
				t.Fatalf("limits %+v were accepted", limits)
			}
			if _, err := NewChannelRegistry(limits, 1, 1); err == nil {
				t.Fatal("a registry was built from invalid limits")
			}
		})
	}
}

func TestTokenRequestTypeNames(t *testing.T) {
	if TokenRequestIssue.String() != "Issue" || TokenRequestRenew.String() != "Renew" {
		t.Fatal("token request types are misnamed")
	}
}
