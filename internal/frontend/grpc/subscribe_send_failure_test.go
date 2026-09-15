package grpcfrontend

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// The Subscribe stream is tested end to end over a real connection, which
// covers everything except the one thing a real connection will not do on
// demand: fail a Send. Nothing had ever made one fail, so the check on its
// result survived inversion -- and what that inversion produces is a stream
// that ends after its first successful message, or one that keeps calling Send
// on a client that is gone.
//
// Send blocking while the client is behind is the only backpressure there is.
// A failure is the client having gone away, so the stream ends there: it is
// not retried, and the values behind it are not buffered for a client that
// will never read them.

// failingStream accepts a fixed number of messages and then refuses. It stands
// in for a transport that has died mid-stream, which bufconn cannot be made to
// do at a chosen moment.
type failingStream struct {
	grpcgo.ServerStream
	ctx         context.Context
	acceptFirst int
	failure     error

	mu   sync.Mutex
	sent int
}

func (stream *failingStream) Context() context.Context { return stream.ctx }

func (stream *failingStream) Send(*opcdav1.DASubscribeResponse) error {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.sent >= stream.acceptFirst {
		return stream.failure
	}
	stream.sent++
	return nil
}

func (stream *failingStream) count() int {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.sent
}

func (stream *failingStream) SetHeader(metadata.MD) error  { return nil }
func (stream *failingStream) SendHeader(metadata.MD) error { return nil }
func (stream *failingStream) SetTrailer(metadata.MD)       {}
func (stream *failingStream) SendMsg(any) error            { return nil }
func (stream *failingStream) RecvMsg(any) error            { return nil }

func TestASubscribeStreamEndsWhenItCannotSend(t *testing.T) {
	sendFailure := errors.New("the client is gone")
	subscription := newFakeSubscription(subscriptionInfo("s1", "Test/Int32"))
	released := make(chan opcda.SubscriptionID, 1)
	runtime := &testRuntime{
		subscribe: func(context.Context, opcda.SubscribeRequest) (opcda.Subscription, error) {
			return subscription, nil
		},
		unsubscribe: func(_ context.Context, id opcda.SubscriptionID) error {
			released <- id
			return nil
		},
	}
	server := New(runtime, Config{
		MaxItemIDBytes: 64, MaxSubscribeItems: 4, MaxSubscriptionStreams: 2,
		MaxBrowseEntries: 8, RequestDeadline: time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The created message goes out; the first batch of values does not.
	stream := &failingStream{ctx: ctx, acceptFirst: 1, failure: sendFailure}

	finished := make(chan error, 1)
	go func() { finished <- server.Subscribe(subscribeRequest("Test/Int32"), stream) }()

	deadline := time.Now().Add(2 * time.Second)
	for stream.count() < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if stream.count() != 1 {
		t.Fatalf("the created message was not sent (%d messages)", stream.count())
	}

	subscription.push(notificationValue("Test/Int32", 1, 192, time.Now(), true))

	select {
	case err := <-finished:
		if !errors.Is(err, sendFailure) {
			t.Errorf("the stream ended with %v, want the send failure", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stream kept running after the client could not be sent to")
	}
	if got := stream.count(); got != 1 {
		t.Errorf("%d messages were accepted, want only the created one", got)
	}

	// Ending for this reason releases the DA group like any other, which is
	// what stops a dead client from holding a subscription on the source.
	select {
	case id := <-released:
		if id != "s1" {
			t.Errorf("the released subscription was %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Error("the subscription was not released after the stream ended")
	}
}
