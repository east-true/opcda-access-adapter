package opcua

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// The listener serves every connection on its own goroutine and the owning
// application expires stale channels and sessions from another, so every
// service the listener holds is shared across goroutines. This exercises that
// directly.
//
// It is the test that was missing. The channel and session registries carried a
// comment claiming a single-goroutine owner that the listener never provided,
// and nothing checked: two clients connecting at the same time faulted the
// process with "concurrent map read and map write", which is a Go runtime
// fatal error that no recover can catch. Every existing test used one
// connection at a time, so the whole suite passed.
func TestConcurrentClientsShareTheListenerSafely(t *testing.T) {
	runtime := &subscribingRuntime{}
	listener, err := NewListenerWithRuntime(testListenerConfig(), runtime, 1000, 2000)
	if err != nil {
		t.Fatal(err)
	}
	socket, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- listener.Serve(socket) }()
	t.Cleanup(func() {
		_ = listener.Close()
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})

	rights := &opcda.DAAccessRights{Raw: 3, Read: true, Write: true}
	if err := listener.AddressSpace().PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Int32", ItemID: itemID("Test/Int32"),
			CanonicalType: varType(opcda.VTI4), AccessRights: rights},
	}); err != nil {
		t.Fatal(err)
	}

	// The application's own maintenance goroutine runs against the same
	// registries while clients are being served.
	stop := make(chan struct{})
	var maintenance sync.WaitGroup
	maintenance.Add(1)
	go func() {
		defer maintenance.Done()
		for {
			select {
			case <-stop:
				return
			default:
				listener.ExpireStaleChannels(time.Now().UTC())
				listener.InvalidateAddressSpace()
			}
		}
	}()

	var clients sync.WaitGroup
	start := make(chan struct{})
	for worker := 0; worker < 8; worker++ {
		clients.Add(1)
		go func() {
			defer clients.Done()
			<-start
			for round := 0; round < 12; round++ {
				exerciseOneClient(t, socket.Addr().String())
			}
		}()
	}
	close(start)
	clients.Wait()
	close(stop)
	maintenance.Wait()
}

// exerciseOneClient runs a whole session through the services the listener
// shares: the channel registry, the session registry, the address space, the
// browse service and the subscription service.
func exerciseOneClient(t *testing.T, address string) {
	t.Helper()
	client := dialTestClient(t, address)
	// The listener bounds concurrent connections, so each round gives its slot
	// back rather than holding one until the test ends.
	defer func() { _ = client.conn.Close() }()
	client.hello()
	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		// A channel the maintenance goroutine expired between calls is a
		// legitimate outcome; a fault is not.
		return
	}
	created, err := client.createSession(opened.SecurityToken, 2, testClientNonce())
	if err != nil {
		return
	}
	if _, err := client.activateSession(opened.SecurityToken, 3,
		created.AuthenticationToken, NullExtensionObject()); err != nil {
		return
	}

	encode := func(write func(*Encoder)) []byte {
		t.Helper()
		encoder, encodeErr := NewEncoder(client.limits)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		write(encoder)
		body, bodyErr := encoder.Bytes()
		if bodyErr != nil {
			t.Fatal(bodyErr)
		}
		return body
	}

	_, _, _ = client.callService(opened.SecurityToken, 4, encode(func(e *Encoder) {
		e.WriteBrowseRequest(BrowseRequest{
			Header:        requestHeaderFor(created.AuthenticationToken, 4),
			NodesToBrowse: []BrowseDescription{browseAll(NumericNodeID(0, NodeIDObjectsFolder))},
		})
	}))
	_, _, _ = client.callService(opened.SecurityToken, 5, encode(func(e *Encoder) {
		e.WriteReadRequest(ReadRequest{
			Header:             requestHeaderFor(created.AuthenticationToken, 5),
			TimestampsToReturn: TimestampsBoth,
			NodesToRead: []ReadValueID{{
				NodeID: NumericNodeID(0, NodeIDServerStatusState), AttributeID: AttributeValue}},
		})
	}))
	_, _, _ = client.callService(opened.SecurityToken, 6, encode(func(e *Encoder) {
		e.WriteCloseSessionRequest(CloseSessionRequest{
			Header: requestHeaderFor(created.AuthenticationToken, 6),
		})
	}))
}

// Every service the listener shares must be safe to use from more than one
// goroutine. Two of them were not, and said so in a comment instead. This
// pins the contract so a future component cannot quietly opt out of it.
func TestSharedServicesAreConcurrencySafe(t *testing.T) {
	registry, err := NewChannelRegistry(DefaultChannelLimits(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := NewSessionRegistry(DefaultSessionLimits())
	if err != nil {
		t.Fatal(err)
	}
	space := testAddressSpace(t)

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 50; round++ {
				channel, issueErr := registry.Issue(SecurityModeNone, 60_000, channelEpoch)
				if issueErr != nil {
					continue
				}
				_, _ = registry.Lookup(channel.ID)
				_, _ = registry.Accept(channel.ID, channel.Token.TokenID, channelEpoch)
				_, _ = registry.Renew(channel.ID, 60_000, channelEpoch)

				session, _, createErr := sessions.Create(
					channel.ID, testSessionSecurity(), testCreateSessionRequest(), channelEpoch)
				if createErr == nil {
					_, _ = sessions.Activate(session.AuthenticationToken, channel.ID,
						ActivateSessionRequest{UserIdentityToken: NullExtensionObject()},
						"", channelEpoch)
					_, _ = sessions.Lookup(session.AuthenticationToken, channel.ID, channelEpoch)
					_ = sessions.Close(session.AuthenticationToken, channel.ID, channelEpoch)
				}
				_ = registry.Close(channel.ID)

				registry.ExpireStale(channelEpoch.Add(time.Hour))
				sessions.ExpireStale(channelEpoch.Add(time.Hour))
				_ = registry.Count()
				_ = sessions.Count()
				_ = space.NodeCount()
				_ = space.SourceNodeCount()
			}
		}()
	}
	wg.Wait()
}

// A session's subscriptions hold DA groups open on the source, so ending the
// session must release them whichever route ended it. The release used to live
// at the CloseSession call site, so a session that timed out left its groups
// open on the source forever — a client that crashes or loses its network is
// the ordinary way that happens.
func TestSessionEndReleasesDAGroupsByEveryRoute(t *testing.T) {
	for _, testCase := range []struct {
		name string
		end  func(t *testing.T, sessions *SessionRegistry, session SessionInfo)
	}{
		{
			name: "an explicit CloseSession",
			end: func(t *testing.T, sessions *SessionRegistry, session SessionInfo) {
				if err := sessions.Close(session.AuthenticationToken, session.ChannelID, channelEpoch); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "the session timing out",
			end: func(t *testing.T, sessions *SessionRegistry, session SessionInfo) {
				if removed := sessions.ExpireStale(channelEpoch.Add(24 * time.Hour)); removed != 1 {
					t.Fatalf("sessions expired = %d, want 1", removed)
				}
			},
		},
		{
			name: "a lookup finding it already expired",
			end: func(t *testing.T, sessions *SessionRegistry, session SessionInfo) {
				_, err := sessions.Lookup(session.AuthenticationToken, session.ChannelID,
					channelEpoch.Add(24*time.Hour))
				if err == nil {
					t.Fatal("an expired session was resolved")
				}
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := &subscribingRuntime{}
			subs, _ := testSubscriptionService(t, runtime)
			sessions, err := NewSessionRegistry(DefaultSessionLimits())
			if err != nil {
				t.Fatal(err)
			}
			sessions.OnSessionEnd(func(ended SessionInfo) {
				subs.ReleaseSession(context.Background(), ended.Key())
			})

			session, _, err := sessions.Create(1, testSessionSecurity(), testCreateSessionRequest(), channelEpoch)
			if err != nil {
				t.Fatal(err)
			}
			id := createSubscriptionFor(t, subs, session.Key())
			monitorItemFor(t, subs, session.Key(), id, ItemNodeID("Test/Int32"), 7)
			if runtime.unsubscribeCount() != 0 {
				t.Fatal("the DA group was released before the session ended")
			}

			testCase.end(t, sessions, session)

			if got := runtime.unsubscribeCount(); got != 1 {
				t.Fatalf("DA unsubscribes = %d, want the group released with the session", got)
			}
			if got := subs.Count(); got != 0 {
				t.Fatalf("the service still holds %d subscriptions", got)
			}
		})
	}
}

// The registry calls its end hook by every route, but a listener that forgets
// to register one leaks just the same. This drives a real listener: a client
// creates a subscription, goes away without closing its session, and the
// session is expired the way the owning application expires it.
func TestListenerReleasesDAGroupsWhenASessionTimesOut(t *testing.T) {
	runtime := &subscribingRuntime{}
	config := testListenerConfig()
	// A session that times out almost at once, so the expiry the application
	// drives is the thing that ends it.
	config.Sessions.MinTimeout = time.Millisecond
	config.Sessions.MaxTimeout = 10 * time.Millisecond
	listener, err := NewListenerWithRuntime(config, runtime, 1000, 2000)
	if err != nil {
		t.Fatal(err)
	}
	socket, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- listener.Serve(socket) }()
	t.Cleanup(func() {
		_ = listener.Close()
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})

	rights := &opcda.DAAccessRights{Raw: 3, Read: true, Write: true}
	if err := listener.AddressSpace().PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Int32", ItemID: itemID("Test/Int32"),
			CanonicalType: varType(opcda.VTI4), AccessRights: rights},
	}); err != nil {
		t.Fatal(err)
	}

	client := dialTestClient(t, socket.Addr().String())
	client.hello()
	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.createSession(opened.SecurityToken, 2, testClientNonce())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.activateSession(opened.SecurityToken, 3,
		created.AuthenticationToken, NullExtensionObject()); err != nil {
		t.Fatal(err)
	}

	encode := func(write func(*Encoder)) []byte {
		t.Helper()
		encoder, encodeErr := NewEncoder(client.limits)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		write(encoder)
		body, bodyErr := encoder.Bytes()
		if bodyErr != nil {
			t.Fatal(bodyErr)
		}
		return body
	}

	identifier, decoder, err := client.callService(opened.SecurityToken, 4, encode(func(e *Encoder) {
		e.WriteCreateSubscriptionRequest(CreateSubscriptionRequest{
			Header:                      requestHeaderFor(created.AuthenticationToken, 4),
			RequestedPublishingInterval: 250,
			RequestedMaxKeepAliveCount:  3,
			PublishingEnabled:           true,
		})
	}))
	if err != nil || identifier != CreateSubscriptionResponseEncodingID {
		t.Fatalf("CreateSubscription: %v (service %d)", err, identifier)
	}
	subscription, err := decoder.ReadCreateSubscriptionResponse()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = client.callService(opened.SecurityToken, 5, encode(func(e *Encoder) {
		e.WriteCreateMonitoredItemsRequest(CreateMonitoredItemsRequest{
			Header:             requestHeaderFor(created.AuthenticationToken, 5),
			SubscriptionID:     subscription.SubscriptionID,
			TimestampsToReturn: TimestampsBoth,
			ItemsToCreate: []MonitoredItemCreateRequest{{
				ItemToMonitor: ReadValueID{
					NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue},
				MonitoringMode: MonitoringModeReporting,
				RequestedParameters: MonitoringParameters{
					ClientHandle: 77, Filter: NullExtensionObject()},
			}},
		})
	})); err != nil {
		t.Fatal(err)
	}
	if runtime.unsubscribeCount() != 0 {
		t.Fatal("the DA group was released while the session was live")
	}

	// The client goes away without closing its session, which is what a crash
	// or a lost network looks like. The application's maintenance pass expires
	// it, and that must release the group on the source.
	_ = client.conn.Close()
	listener.ExpireStaleChannels(time.Now().UTC().Add(time.Hour))

	if got := runtime.unsubscribeCount(); got != 1 {
		t.Fatalf("DA unsubscribes = %d, want the group released when the session timed out", got)
	}
}
