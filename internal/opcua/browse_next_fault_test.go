package opcua

import "testing"

// A BrowseNext that names no continuation point has nothing to continue, and
// the browse service says so with Bad_NothingToDo. The listener has to relay
// that as a service fault rather than treating it as an answer: the dispatch
// arm that checks it survived inversion, which would have written a
// BrowseNextResponse built from an error the service never filled in.
//
// The channel stays open either way, which is what makes the difference
// visible only in what the client is told.
func TestABrowseNextWithNothingToContinueFaults(t *testing.T) {
	listener, address := startTestListener(t, testListenerConfig())
	_ = listener
	client := dialTestClient(t, address)
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

	encoder, err := NewEncoder(client.limits)
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteBrowseNextRequest(BrowseNextRequest{
		Header: requestHeaderFor(created.AuthenticationToken, 4),
	})
	serviceBody, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	identifier, decoder, err := client.callService(opened.SecurityToken, 4, serviceBody)
	if err != nil {
		t.Fatalf("the channel was closed instead of faulting: %v", err)
	}
	if identifier != ServiceFaultEncodingID {
		t.Fatalf("service = %d, want a ServiceFault", identifier)
	}
	header, err := decoder.ReadResponseHeader()
	if err != nil {
		t.Fatal(err)
	}
	if header.ServiceResult != StatusBadNothingToDo {
		t.Errorf("service result = %s, want Bad_NothingToDo", header.ServiceResult.Hex())
	}
	if header.RequestHandle != 4 {
		t.Errorf("the fault carries request handle %d, not the one that was sent", header.RequestHandle)
	}
}

// sessionSecurity describes the channel a request arrived on, and a channel
// that is not there cannot be described. Every caller reaches it on a live
// channel, so the lookup failing is defensive -- but what the guard prevents is
// a session recorded against security settings that are the zero value, which
// ActivateSession would then compare against and accept.
func TestDescribingAnAbsentChannelFails(t *testing.T) {
	listener, address := startTestListener(t, testListenerConfig())
	client := dialTestClient(t, address)
	client.hello()
	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}

	// The control: the channel that was just opened can be described.
	security, err := listener.sessionSecurity(opened.SecurityToken.SecureChannelID)
	if err != nil {
		t.Fatalf("an open channel could not be described: %v", err)
	}
	if security.PolicyURI == "" {
		t.Error("an open channel was described without a security policy")
	}

	// An identifier no channel was issued. Channel ids are assigned in
	// sequence from the registry, so one far past what this listener has
	// handed out belongs to nothing.
	if _, err := listener.sessionSecurity(opened.SecurityToken.SecureChannelID + 1_000_000); err == nil {
		t.Error("a channel that was never opened was described")
	}

}
