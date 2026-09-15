package opcua

import (
	"errors"
	"fmt"
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// Table 74 has the server refuse a response that *exceeds* the MaxMessageSize
// the client asked for. One that is exactly that size does not exceed it and
// must be sent: the number a client puts in its Hello is the size it has
// sized its buffers for, so refusing a response that fits is the server
// breaking the same promise from the other side.
//
// The existing cases browse two hundred items against a limit of two thousand
// bytes, and browse the same against no limit at all -- clearly over and
// clearly unlimited, never the value that decides. The bound survived being
// tightened by one, which would refuse every response that filled the client's
// buffer exactly.
//
// The exact size is measured rather than guessed: one connection with no limit
// reads the response and reports how long its service body is, and the two
// connections after it ask for exactly that and for one byte less.
func TestAResponseThatExactlyFillsTheClientsLimitIsSent(t *testing.T) {
	entries := make([]opcda.BrowseEntry, 0, 40)
	for index := 0; index < 40; index++ {
		name := fmt.Sprintf("Item%03d", index)
		entries = append(entries, opcda.BrowseEntry{
			Kind: opcda.BrowseEntryItem, Name: name, ItemID: itemID(name),
		})
	}

	listener, address := startTestListener(t, testListenerConfig())
	if err := listener.AddressSpace().PopulateBranch(nil, entries); err != nil {
		t.Fatal(err)
	}

	// browseWith runs one whole connection and returns the service body the
	// server sent, or the error it refused with.
	browseWith := func(t *testing.T, maxMessage uint32) ([]byte, error) {
		t.Helper()
		client := dialTestClient(t, address)
		client.helloWithMaxMessage(maxMessage)
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
		encoder.WriteBrowseRequest(BrowseRequest{
			Header:        requestHeaderFor(created.AuthenticationToken, 4),
			NodesToBrowse: []BrowseDescription{browseAll(listener.AddressSpace().SourceFolderID())},
		})
		request, err := encoder.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		client.sendService(opened.SecurityToken, 4, request)

		header, response, err := client.receive()
		if err != nil {
			return nil, err
		}
		if header.Type == MessageTypeError {
			protocolError, decodeErr := DecodeProtocolError(response, client.limits)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			return nil, &CodecError{Status: protocolError.Error, Message: protocolError.Reason}
		}
		// The size the server measures is the service body: what follows the
		// SecureChannelId, TokenId and sequence header.
		return response[8+SequenceHeaderSize:], nil
	}

	body, err := browseWith(t, 0)
	if err != nil {
		t.Fatalf("a browse against no limit was refused: %v", err)
	}
	size := uint32(len(body))
	if size == 0 {
		t.Fatal("the unlimited browse returned an empty body, so there is no size to bound")
	}

	t.Run("exactly the size the client accepted", func(t *testing.T) {
		exact, err := browseWith(t, size)
		if err != nil {
			t.Fatalf("a response of exactly the %d bytes the client accepted was refused: %v", size, err)
		}
		if uint32(len(exact)) != size {
			t.Errorf("the bounded response is %d bytes, not the %d the unlimited one was",
				len(exact), size)
		}
	})

	t.Run("one byte more than the client accepted", func(t *testing.T) {
		_, err := browseWith(t, size-1)
		if err == nil {
			t.Fatalf("a response of %d bytes was sent to a client accepting %d", size, size-1)
		}
		var codecErr *CodecError
		if !errors.As(err, &codecErr) || codecErr.Status != StatusBadResponseTooLarge {
			t.Fatalf("error = %v, want Bad_ResponseTooLarge", err)
		}
	})
}
