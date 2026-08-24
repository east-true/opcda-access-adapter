package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"
)

func TestOverloadClosesIdleConnectionsBeforeSaturation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	bounded := newProbeBoundedListener(listener, 4)
	warmEntered := make(chan struct{}, 3)
	warmRelease := make(chan struct{})
	requestSlots := make(chan struct{}, 2)
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/warm" {
			warmEntered <- struct{}{}
			<-warmRelease
			response.WriteHeader(http.StatusNoContent)
			return
		}
		select {
		case requestSlots <- struct{}{}:
			defer func() { <-requestSlots }()
		default:
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(response, `{"error":{"layer":"frontend","code":"QUEUE_FULL"}}`)
			return
		}
		switch request.URL.Path {
		case "/v1/status":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"state":"connected","source":{"connectionGeneration":1},"frontend":{"http":{"listening":true}}}`)
		case "/v1/read":
			_, _ = io.Copy(io.Discard, request.Body)
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"results":[{"itemId":"Test/Int32","ok":true}]}`)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	})
	server := &http.Server{Handler: handler, ReadTimeout: 10 * time.Second}
	go func() { _ = server.Serve(bounded) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})

	transport := &http.Transport{
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 8,
		MaxConnsPerHost:     8,
	}
	p := probe{
		baseURL: &url.URL{Scheme: "http", Host: listener.Addr().String()},
		client:  &http.Client{Transport: transport, Timeout: 5 * time.Second},
	}
	t.Cleanup(transport.CloseIdleConnections)

	warmErrors := make(chan error, 3)
	for index := 0; index < 3; index++ {
		go func() {
			status, _, requestErr := p.request(http.MethodGet, "/warm", nil, nil)
			if requestErr != nil {
				warmErrors <- requestErr
				return
			}
			if status != http.StatusNoContent {
				warmErrors <- fmt.Errorf("warm request returned HTTP %d", status)
				return
			}
			warmErrors <- nil
		}()
	}
	for index := 0; index < 3; index++ {
		select {
		case <-warmEntered:
		case <-time.After(2 * time.Second):
			t.Fatal("warm requests did not establish three connections")
		}
	}
	close(warmRelease)
	for index := 0; index < 3; index++ {
		if err := <-warmErrors; err != nil {
			t.Fatal(err)
		}
	}
	if active := bounded.active(); active != 3 {
		t.Fatalf("test precondition: got %d retained connections, want 3", active)
	}

	if err := p.overload(4, 2); err != nil {
		t.Fatal(err)
	}
}

func TestSlowBodiesAreClosedByServerReadTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/status":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"state":"connected","source":{"connectionGeneration":1},"frontend":{"http":{"listening":true}}}`)
		case "/v1/read":
			_, _ = io.Copy(io.Discard, request.Body)
			response.WriteHeader(http.StatusNoContent)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	})
	server := &http.Server{Handler: handler, ReadTimeout: 100 * time.Millisecond}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})

	transport := &http.Transport{MaxIdleConns: 4, MaxIdleConnsPerHost: 4}
	p := probe{
		baseURL: &url.URL{Scheme: "http", Host: listener.Addr().String()},
		client:  &http.Client{Transport: transport, Timeout: 2 * time.Second},
	}
	t.Cleanup(transport.CloseIdleConnections)
	if err := p.slowBodies(2, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func TestMalformedHTTPProtocolRequestsAreRejected(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/status" {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"state":"connected","source":{"connectionGeneration":1},"frontend":{"http":{"listening":true}}}`)
	})
	server := &http.Server{Handler: handler, ReadTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})

	transport := &http.Transport{MaxIdleConns: 4, MaxIdleConnsPerHost: 4}
	p := probe{
		baseURL: &url.URL{Scheme: "http", Host: listener.Addr().String()},
		client:  &http.Client{Transport: transport, Timeout: 2 * time.Second},
	}
	t.Cleanup(transport.CloseIdleConnections)
	if err := p.protocolAnomalies(); err != nil {
		t.Fatal(err)
	}
}

type probeBoundedListener struct {
	net.Listener
	permits chan struct{}
}

func newProbeBoundedListener(listener net.Listener, maximum int) *probeBoundedListener {
	return &probeBoundedListener{Listener: listener, permits: make(chan struct{}, maximum)}
}

func (listener *probeBoundedListener) Accept() (net.Conn, error) {
	for {
		connection, err := listener.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case listener.permits <- struct{}{}:
			return &probeBoundedConnection{Conn: connection, release: func() { <-listener.permits }}, nil
		default:
			_ = connection.Close()
		}
	}
}

func (listener *probeBoundedListener) active() int {
	return len(listener.permits)
}

type probeBoundedConnection struct {
	net.Conn
	once    sync.Once
	release func()
}

func (connection *probeBoundedConnection) Close() error {
	err := connection.Conn.Close()
	connection.once.Do(connection.release)
	return err
}
