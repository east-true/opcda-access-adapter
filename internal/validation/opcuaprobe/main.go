// Command opcuaprobe exercises the OPC UA frontend against a real local OPC DA
// server. It is validation tooling only and never logs a process value; only UA
// status codes, node identifiers, and counts are printed.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcua"
)

const maximumProbeTimeout = 5 * time.Minute

func main() {
	address := flag.String("address", "127.0.0.1:4840", "OPC UA endpoint address")
	endpointURL := flag.String("endpoint-url", "", "endpoint URL the server publishes")
	policyURI := flag.String("security-policy-uri", "", "expected SecurityPolicy URI")
	writeEnabled := flag.Bool("write-enabled", false,
		"expect Write to be enabled and drive the subscription change scenario")
	timeout := flag.Duration("timeout", time.Minute, "bounded scenario deadline")
	flag.Parse()
	if flag.NArg() != 0 || *endpointURL == "" || *policyURI == "" ||
		*timeout <= 0 || *timeout > maximumProbeTimeout {
		fmt.Fprintln(os.Stderr,
			"usage: opcuaprobe -address HOST:PORT -endpoint-url URL -security-policy-uri URI [-timeout DURATION]")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := run(ctx, *address, *endpointURL, *policyURI, *writeEnabled); err != nil {
		fmt.Fprintf(os.Stderr, "OPCUA_REAL_DA_FAIL %v\n", err)
		os.Exit(1)
	}
}

// dialWhenReady retries until the adapter's listener accepts or the deadline
// passes. The adapter is started moments earlier by the validation harness, so
// the first connection attempt is refused rather than timing out.
func dialWhenReady(ctx context.Context, address string) (net.Conn, error) {
	var lastErr error
	for {
		conn, err := net.DialTimeout("tcp", address, 5*time.Second)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("the OPC UA listener did not accept a connection: %w", lastErr)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// client is a minimal UA-TCP client: enough to complete the connection sequence
// and call the services this adapter implements.
type client struct {
	conn     net.Conn
	limits   opcua.BinaryLimits
	sequence uint32
	buffer   uint32
}

func run(ctx context.Context, address, endpointURL, policyURI string, writeEnabled bool) error {
	deadline, _ := ctx.Deadline()
	conn, err := dialWhenReady(ctx, address)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	c := &client{conn: conn, limits: opcua.DefaultBinaryLimits(), buffer: 65536}

	ack, err := c.hello(endpointURL)
	if err != nil {
		return err
	}
	if ack.ProtocolVersion != opcua.ProtocolVersion {
		return fmt.Errorf("the server acknowledged protocol version %d", ack.ProtocolVersion)
	}
	fmt.Printf("opcua hello acknowledged receiveBuffer=%d sendBuffer=%d\n",
		ack.ReceiveBufferSize, ack.SendBufferSize)

	token, err := c.openChannel()
	if err != nil {
		return err
	}
	if token.SecureChannelID == 0 || token.TokenID == 0 || token.RevisedLifetime == 0 {
		return fmt.Errorf("the server issued an incomplete security token")
	}
	fmt.Printf("opcua channel opened channel=%d revisedLifetime=%dms\n",
		token.SecureChannelID, token.RevisedLifetime)

	endpoints, err := c.getEndpoints(token, endpointURL)
	if err != nil {
		return err
	}
	if len(endpoints) != 1 {
		return fmt.Errorf("the server published %d endpoints, want 1", len(endpoints))
	}
	endpoint := endpoints[0]
	if endpoint.EndpointURL != endpointURL {
		return fmt.Errorf("the published endpoint URL did not match the configured one")
	}
	if endpoint.SecurityPolicyURI != policyURI {
		return fmt.Errorf("the published security policy URI did not match the configured one")
	}
	if endpoint.SecurityMode != opcua.SecurityModeNone {
		return fmt.Errorf("the published security mode was not None")
	}
	// An unsecured endpoint carries no certificate and is not recommended.
	if endpoint.ServerCertificate != nil {
		return fmt.Errorf("an unsecured endpoint published a certificate")
	}
	if endpoint.SecurityLevel != 0 {
		return fmt.Errorf("an unsecured endpoint published security level %d", endpoint.SecurityLevel)
	}
	fmt.Printf("opcua endpoint verified securityMode=None securityLevel=%d certificate=absent\n",
		endpoint.SecurityLevel)

	session, err := c.createSession(token, endpointURL)
	if err != nil {
		return err
	}
	if err := c.activateSession(token, session); err != nil {
		return err
	}
	fmt.Print("opcua session created and activated\n")

	sourceFolder, err := c.browseRoot(token, session)
	if err != nil {
		return err
	}
	items, err := c.browseForItems(token, session, sourceFolder)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("the address space exposed no DA items")
	}
	fmt.Printf("opcua address space populated from the source items=%d\n", len(items))

	if err := c.readItem(token, session, items[0]); err != nil {
		return err
	}

	subscribed := false
	if writeEnabled {
		// The fixture's Test items are static, so a subscription would only
		// ever see the server's initial snapshot. Writing through the UA Write
		// service induces real changes, which is the only way to prove the
		// notification path carries source-driven data rather than one
		// snapshot.
		if err := c.subscriptionScenario(ctx, token, session, items); err != nil {
			return err
		}
		subscribed = true
	}
	fmt.Printf(
		"OPCUA_REAL_DA_PASS endpoint=verified session=activated items=%d read=ok subscription=%t valuesLogged=false\n",
		len(items), subscribed)
	return nil
}

// writableItem finds the node standing for the fixture's read/write VT_R4 item.
//
// A browsed node reports the abstract base type and no rights, because OPC DA
// carries both in the AddItems result rather than in Browse. Reading an item is
// what teaches the server, so the probe reads each candidate first and then
// asks for its attributes — which is what a real client does too.
func (c *client) writableItem(token opcua.ChannelSecurityToken, session opcua.NodeID, items []opcua.NodeID) (opcua.NodeID, error) {
	for _, item := range items {
		if err := c.readItem(token, session, item); err != nil {
			// An item the source refuses to read is simply not the target.
			continue
		}
		dataType, level, err := c.itemAttributes(token, session, item)
		if err != nil {
			return opcua.NodeID{}, err
		}
		if dataType.Numeric != opcua.NodeIDFloat {
			continue
		}
		if level&opcua.AccessLevelCurrentWrite == 0 {
			continue
		}
		return item, nil
	}
	return opcua.NodeID{}, fmt.Errorf("the address space exposed no writable VT_R4 item")
}

// itemAttributes reads the DataType and AccessLevel a node reports.
func (c *client) itemAttributes(token opcua.ChannelSecurityToken, session opcua.NodeID, node opcua.NodeID) (opcua.NodeID, byte, error) {
	encoder, err := opcua.NewEncoder(c.limits)
	if err != nil {
		return opcua.NodeID{}, 0, err
	}
	encoder.WriteReadRequest(opcua.ReadRequest{
		Header:             requestHeader(session, 50),
		TimestampsToReturn: opcua.TimestampsBoth,
		NodesToRead: []opcua.ReadValueID{
			{NodeID: node, AttributeID: opcua.AttributeDataType},
			{NodeID: node, AttributeID: opcua.AttributeAccessLevel},
		},
	})
	body, err := encoder.Bytes()
	if err != nil {
		return opcua.NodeID{}, 0, err
	}
	identifier, decoder, err := c.call(token, 50, body)
	if err != nil {
		return opcua.NodeID{}, 0, err
	}
	if identifier != opcua.ReadResponseEncodingID {
		return opcua.NodeID{}, 0, fmt.Errorf("attribute read answered with service %d", identifier)
	}
	response, err := decoder.ReadReadResponse()
	if err != nil {
		return opcua.NodeID{}, 0, err
	}
	if len(response.Results) != 2 {
		return opcua.NodeID{}, 0, fmt.Errorf("attribute read returned %d results", len(response.Results))
	}
	dataType, ok := response.Results[0].Value.Value.(opcua.NodeID)
	if !ok {
		return opcua.NodeID{}, 0, fmt.Errorf("the DataType attribute was not a NodeId")
	}
	level, ok := response.Results[1].Value.Value.(byte)
	if !ok {
		return opcua.NodeID{}, 0, fmt.Errorf("the AccessLevel attribute was not a Byte")
	}
	return dataType, level, nil
}

// subscriptionScenario creates a subscription over the writable item, then
// writes distinct values and requires each one to arrive through Publish.
func (c *client) subscriptionScenario(ctx context.Context, token opcua.ChannelSecurityToken, session opcua.NodeID, items []opcua.NodeID) error {
	target, err := c.writableItem(token, session, items)
	if err != nil {
		return err
	}

	subscription, err := c.createSubscription(token, session)
	if err != nil {
		return err
	}
	if subscription.SubscriptionID == 0 || subscription.RevisedPublishingInterval <= 0 {
		return fmt.Errorf("the server returned an incomplete subscription")
	}
	// Table 82: the lifetime count is at least three times the keep-alive
	// count.
	if subscription.RevisedLifetimeCount < subscription.RevisedMaxKeepAliveCount*3 {
		return fmt.Errorf("revised lifetime %d is below three times the keep-alive %d",
			subscription.RevisedLifetimeCount, subscription.RevisedMaxKeepAliveCount)
	}
	fmt.Printf("opcua subscription created id=%d revisedInterval=%vms keepAlive=%d\n",
		subscription.SubscriptionID, subscription.RevisedPublishingInterval,
		subscription.RevisedMaxKeepAliveCount)

	const clientHandle = uint32(4242)
	created, err := c.createMonitoredItem(token, session, subscription.SubscriptionID, target, clientHandle)
	if err != nil {
		return err
	}
	if created.StatusCode != opcua.StatusGood {
		return fmt.Errorf("the monitored item was refused with %s", created.StatusCode.Hex())
	}
	// The DA core coalesces per item, so the queue is one value per item.
	if created.RevisedQueueSize != 1 {
		return fmt.Errorf("the server revised the queue size to %d", created.RevisedQueueSize)
	}
	fmt.Printf("opcua monitored item created id=%d revisedSampling=%vms queue=%d\n",
		created.MonitoredItemID, created.RevisedSamplingInterval, created.RevisedQueueSize)

	// The server's initial snapshot proves the notification path is live.
	if _, err := c.awaitNotification(ctx, token, session, clientHandle, nil); err != nil {
		return fmt.Errorf("initial notification: %w", err)
	}

	const changes = 2
	for change := 0; change < changes; change++ {
		written := float32(change+1) + 0.75
		if err := c.writeFloat(token, session, target, written); err != nil {
			return fmt.Errorf("write to induce change %d: %w", change+1, err)
		}
		expected := written
		if _, err := c.awaitNotification(ctx, token, session, clientHandle, &expected); err != nil {
			return fmt.Errorf("change %d was not published: %w", change+1, err)
		}
	}
	fmt.Printf("opcua change-driven notifications verified changes=%d\n", changes)

	if err := c.deleteSubscription(token, session, subscription.SubscriptionID); err != nil {
		return err
	}
	fmt.Print("opcua subscription deleted\n")
	return nil
}

func (c *client) createSubscription(token opcua.ChannelSecurityToken, session opcua.NodeID) (opcua.CreateSubscriptionResponse, error) {
	encoder, err := opcua.NewEncoder(c.limits)
	if err != nil {
		return opcua.CreateSubscriptionResponse{}, err
	}
	encoder.WriteCreateSubscriptionRequest(opcua.CreateSubscriptionRequest{
		Header:                      requestHeader(session, 51),
		RequestedPublishingInterval: 250,
		RequestedMaxKeepAliveCount:  3,
		RequestedLifetimeCount:      30,
		PublishingEnabled:           true,
	})
	body, err := encoder.Bytes()
	if err != nil {
		return opcua.CreateSubscriptionResponse{}, err
	}
	identifier, decoder, err := c.call(token, 51, body)
	if err != nil {
		return opcua.CreateSubscriptionResponse{}, fmt.Errorf("create subscription: %w", err)
	}
	if identifier != opcua.CreateSubscriptionResponseEncodingID {
		return opcua.CreateSubscriptionResponse{}, fmt.Errorf("create subscription answered with service %d", identifier)
	}
	return decoder.ReadCreateSubscriptionResponse()
}

func (c *client) createMonitoredItem(token opcua.ChannelSecurityToken, session opcua.NodeID, subscriptionID uint32, node opcua.NodeID, handle uint32) (opcua.MonitoredItemCreateResult, error) {
	encoder, err := opcua.NewEncoder(c.limits)
	if err != nil {
		return opcua.MonitoredItemCreateResult{}, err
	}
	encoder.WriteCreateMonitoredItemsRequest(opcua.CreateMonitoredItemsRequest{
		Header:             requestHeader(session, 52),
		SubscriptionID:     subscriptionID,
		TimestampsToReturn: opcua.TimestampsBoth,
		ItemsToCreate: []opcua.MonitoredItemCreateRequest{{
			ItemToMonitor:  opcua.ReadValueID{NodeID: node, AttributeID: opcua.AttributeValue},
			MonitoringMode: opcua.MonitoringModeReporting,
			RequestedParameters: opcua.MonitoringParameters{
				ClientHandle: handle, SamplingInterval: 250, QueueSize: 1,
				Filter: opcua.NullExtensionObject(),
			},
		}},
	})
	body, err := encoder.Bytes()
	if err != nil {
		return opcua.MonitoredItemCreateResult{}, err
	}
	identifier, decoder, err := c.call(token, 52, body)
	if err != nil {
		return opcua.MonitoredItemCreateResult{}, fmt.Errorf("create monitored items: %w", err)
	}
	if identifier != opcua.CreateMonitoredItemsResponseEncodingID {
		return opcua.MonitoredItemCreateResult{}, fmt.Errorf("create monitored items answered with service %d", identifier)
	}
	response, err := decoder.ReadCreateMonitoredItemsResponse()
	if err != nil {
		return opcua.MonitoredItemCreateResult{}, err
	}
	if len(response.Results) != 1 {
		return opcua.MonitoredItemCreateResult{}, fmt.Errorf("create monitored items returned %d results", len(response.Results))
	}
	return response.Results[0], nil
}

// awaitNotification polls Publish until a notification for the handle arrives.
// Publish does not block on this server, so the client polls; the wait is
// bounded by the scenario deadline. When expected is supplied the notification
// must carry that value, which is how a change is told from the snapshot that
// preceded it.
func (c *client) awaitNotification(ctx context.Context, token opcua.ChannelSecurityToken, session opcua.NodeID, handle uint32, expected *float32) (opcua.DataValue, error) {
	deadline := time.Now().Add(30 * time.Second)
	handleFound := false
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return opcua.DataValue{}, ctx.Err()
		default:
		}
		response, err := c.publish(token, session)
		if err != nil {
			return opcua.DataValue{}, err
		}
		for _, notification := range response.NotificationMessage.Notifications {
			if notification.ClientHandle != handle {
				continue
			}
			handleFound = true
			if notification.Value.Status.IsBad() {
				return opcua.DataValue{}, fmt.Errorf(
					"the notification carried %s", notification.Value.Status.Hex())
			}
			if expected == nil {
				return notification.Value, nil
			}
			value, ok := notification.Value.Value.Value.(float32)
			if ok && value == *expected {
				return notification.Value, nil
			}
			// An older coalesced value can still be in flight; keep polling.
		}
		time.Sleep(100 * time.Millisecond)
	}
	if handleFound {
		return opcua.DataValue{}, fmt.Errorf("the expected value never arrived for handle %d", handle)
	}
	return opcua.DataValue{}, fmt.Errorf("no notification arrived for handle %d", handle)
}

func (c *client) publish(token opcua.ChannelSecurityToken, session opcua.NodeID) (opcua.PublishResponse, error) {
	encoder, err := opcua.NewEncoder(c.limits)
	if err != nil {
		return opcua.PublishResponse{}, err
	}
	encoder.WritePublishRequest(opcua.PublishRequest{Header: requestHeader(session, 53)})
	body, err := encoder.Bytes()
	if err != nil {
		return opcua.PublishResponse{}, err
	}
	identifier, decoder, err := c.call(token, 53, body)
	if err != nil {
		return opcua.PublishResponse{}, fmt.Errorf("publish: %w", err)
	}
	if identifier != opcua.PublishResponseEncodingID {
		return opcua.PublishResponse{}, fmt.Errorf("publish answered with service %d", identifier)
	}
	return decoder.ReadPublishResponse()
}

func (c *client) writeFloat(token opcua.ChannelSecurityToken, session opcua.NodeID, node opcua.NodeID, value float32) error {
	encoder, err := opcua.NewEncoder(c.limits)
	if err != nil {
		return err
	}
	encoder.WriteWriteRequest(opcua.WriteRequest{
		Header: requestHeader(session, 54),
		NodesToWrite: []opcua.WriteValue{{
			NodeID: node, AttributeID: opcua.AttributeValue,
			Value: opcua.DataValue{
				Value: opcua.Variant{Type: opcua.BuiltInFloat, Value: value},
			},
		}},
	})
	body, err := encoder.Bytes()
	if err != nil {
		return err
	}
	identifier, decoder, err := c.call(token, 54, body)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if identifier != opcua.WriteResponseEncodingID {
		return fmt.Errorf("write answered with service %d", identifier)
	}
	response, err := decoder.ReadWriteResponse()
	if err != nil {
		return err
	}
	if len(response.Results) != 1 {
		return fmt.Errorf("write returned %d results", len(response.Results))
	}
	if response.Results[0] != opcua.StatusGood {
		return fmt.Errorf("write returned %s", response.Results[0].Hex())
	}
	return nil
}

func (c *client) deleteSubscription(token opcua.ChannelSecurityToken, session opcua.NodeID, subscriptionID uint32) error {
	encoder, err := opcua.NewEncoder(c.limits)
	if err != nil {
		return err
	}
	encoder.WriteDeleteSubscriptionsRequest(opcua.DeleteSubscriptionsRequest{
		Header:          requestHeader(session, 55),
		SubscriptionIDs: []uint32{subscriptionID},
	})
	body, err := encoder.Bytes()
	if err != nil {
		return err
	}
	identifier, decoder, err := c.call(token, 55, body)
	if err != nil {
		return fmt.Errorf("delete subscriptions: %w", err)
	}
	if identifier != opcua.DeleteSubscriptionsResponseEncodingID {
		return fmt.Errorf("delete subscriptions answered with service %d", identifier)
	}
	response, err := decoder.ReadDeleteSubscriptionsResponse()
	if err != nil {
		return err
	}
	if len(response.Results) != 1 || response.Results[0] != opcua.StatusGood {
		return fmt.Errorf("delete subscriptions returned %v", response.Results)
	}
	return nil
}

func (c *client) send(messageType opcua.MessageType, body []byte) error {
	header, err := opcua.EncodeMessageHeader(messageType, opcua.ChunkFinal, len(body), c.buffer)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(append(header, body...))
	return err
}

func (c *client) receive() (opcua.MessageHeader, []byte, error) {
	var headerBytes [opcua.HeaderSize]byte
	if _, err := io.ReadFull(c.conn, headerBytes[:]); err != nil {
		return opcua.MessageHeader{}, nil, err
	}
	header, err := opcua.DecodeMessageHeader(headerBytes[:], c.buffer)
	if err != nil {
		return opcua.MessageHeader{}, nil, err
	}
	body := make([]byte, header.BodySize())
	if _, err := io.ReadFull(c.conn, body); err != nil {
		return opcua.MessageHeader{}, nil, err
	}
	if header.Type == opcua.MessageTypeError {
		protocolError, decodeErr := opcua.DecodeProtocolError(body, c.limits)
		if decodeErr != nil {
			return opcua.MessageHeader{}, nil, decodeErr
		}
		return opcua.MessageHeader{}, nil, fmt.Errorf(
			"the server reported %s: %s", protocolError.Error.Hex(), protocolError.Reason)
	}
	return header, body, nil
}

func (c *client) hello(endpointURL string) (opcua.Acknowledge, error) {
	body, err := opcua.EncodeHello(opcua.Hello{
		ProtocolVersion:   opcua.ProtocolVersion,
		ReceiveBufferSize: c.buffer,
		SendBufferSize:    c.buffer,
		MaxMessageSize:    1 << 20,
		MaxChunkCount:     16,
		EndpointURL:       endpointURL,
	}, c.limits)
	if err != nil {
		return opcua.Acknowledge{}, err
	}
	if err := c.send(opcua.MessageTypeHello, body); err != nil {
		return opcua.Acknowledge{}, err
	}
	header, response, err := c.receive()
	if err != nil {
		return opcua.Acknowledge{}, fmt.Errorf("hello: %w", err)
	}
	if header.Type != opcua.MessageTypeAcknowledge {
		return opcua.Acknowledge{}, fmt.Errorf("the server answered a Hello with %s", header.Type)
	}
	return opcua.DecodeAcknowledge(response, c.limits)
}

func (c *client) openChannel() (opcua.ChannelSecurityToken, error) {
	service, err := opcua.NewEncoder(c.limits)
	if err != nil {
		return opcua.ChannelSecurityToken{}, err
	}
	service.WriteOpenSecureChannelRequest(opcua.OpenSecureChannelRequest{
		Header: opcua.RequestHeader{
			AuthenticationToken: opcua.NumericNodeID(0, 0),
			Timestamp:           time.Now().UTC(),
			RequestHandle:       1,
			AdditionalHeader:    opcua.NullExtensionObject(),
		},
		ClientProtocolVersion: opcua.ProtocolVersion,
		RequestType:           opcua.TokenRequestIssue,
		SecurityMode:          opcua.SecurityModeNone,
		RequestedLifetime:     60_000,
	})
	serviceBody, err := service.Bytes()
	if err != nil {
		return opcua.ChannelSecurityToken{}, err
	}
	security, err := opcua.EncodeAsymmetricSecurityHeader(opcua.AsymmetricSecurityHeader{}, 0, c.limits)
	if err != nil {
		return opcua.ChannelSecurityToken{}, err
	}
	body, err := opcua.NewEncoder(c.limits)
	if err != nil {
		return opcua.ChannelSecurityToken{}, err
	}
	body.WriteUInt32(0)
	body.WriteByteString(nil)
	encoded, err := body.Bytes()
	if err != nil {
		return opcua.ChannelSecurityToken{}, err
	}
	// The channel id, then the security header, sequence header and body.
	frame := encoded[:4]
	frame = append(frame, security...)
	c.sequence++
	frame = appendUInt32(frame, c.sequence)
	frame = appendUInt32(frame, 1)
	frame = append(frame, serviceBody...)
	if err := c.send(opcua.MessageTypeOpenChannel, frame); err != nil {
		return opcua.ChannelSecurityToken{}, err
	}

	_, response, err := c.receive()
	if err != nil {
		return opcua.ChannelSecurityToken{}, fmt.Errorf("open secure channel: %w", err)
	}
	_, used, err := opcua.DecodeAsymmetricSecurityHeader(response[4:], 4096, c.limits)
	if err != nil {
		return opcua.ChannelSecurityToken{}, err
	}
	decoder, err := opcua.NewDecoder(response[4+used+opcua.SequenceHeaderSize:], c.limits)
	if err != nil {
		return opcua.ChannelSecurityToken{}, err
	}
	identifier, err := decoder.ReadServiceTypeID()
	if err != nil {
		return opcua.ChannelSecurityToken{}, err
	}
	if identifier != opcua.OpenSecureChannelResponseEncodingID {
		return opcua.ChannelSecurityToken{}, fmt.Errorf("the server answered with service %d", identifier)
	}
	opened, err := decoder.ReadOpenSecureChannelResponse()
	if err != nil {
		return opcua.ChannelSecurityToken{}, err
	}
	if opened.Header.ServiceResult != opcua.StatusGood {
		return opcua.ChannelSecurityToken{}, fmt.Errorf(
			"open secure channel returned %s", opened.Header.ServiceResult.Hex())
	}
	return opened.SecurityToken, nil
}

func appendUInt32(data []byte, value uint32) []byte {
	return append(data, byte(value), byte(value>>8), byte(value>>16), byte(value>>24))
}

// call sends a MSG and returns the response service identifier and decoder.
func (c *client) call(token opcua.ChannelSecurityToken, requestID uint32, serviceBody []byte) (uint32, *opcua.Decoder, error) {
	frame := appendUInt32(nil, token.SecureChannelID)
	frame = appendUInt32(frame, token.TokenID)
	c.sequence++
	frame = appendUInt32(frame, c.sequence)
	frame = appendUInt32(frame, requestID)
	frame = append(frame, serviceBody...)
	if err := c.send(opcua.MessageTypeSecure, frame); err != nil {
		return 0, nil, err
	}
	_, response, err := c.receive()
	if err != nil {
		return 0, nil, err
	}
	decoder, err := opcua.NewDecoder(response[8+opcua.SequenceHeaderSize:], c.limits)
	if err != nil {
		return 0, nil, err
	}
	identifier, err := decoder.ReadServiceTypeID()
	if err != nil {
		return 0, nil, err
	}
	if identifier == opcua.ServiceFaultEncodingID {
		header, headerErr := decoder.ReadResponseHeader()
		if headerErr != nil {
			return 0, nil, headerErr
		}
		return 0, nil, fmt.Errorf("the service faulted with %s", header.ServiceResult.Hex())
	}
	return identifier, decoder, nil
}

func requestHeader(authToken opcua.NodeID, handle uint32) opcua.RequestHeader {
	return opcua.RequestHeader{
		AuthenticationToken: authToken,
		Timestamp:           time.Now().UTC(),
		RequestHandle:       handle,
		AdditionalHeader:    opcua.NullExtensionObject(),
	}
}

func (c *client) getEndpoints(token opcua.ChannelSecurityToken, endpointURL string) ([]opcua.EndpointDescription, error) {
	encoder, err := opcua.NewEncoder(c.limits)
	if err != nil {
		return nil, err
	}
	encoder.WriteGetEndpointsRequest(opcua.GetEndpointsRequest{
		Header:      requestHeader(opcua.NumericNodeID(0, 0), 2),
		EndpointURL: endpointURL,
	})
	serviceBody, err := encoder.Bytes()
	if err != nil {
		return nil, err
	}
	identifier, decoder, err := c.call(token, 2, serviceBody)
	if err != nil {
		return nil, fmt.Errorf("get endpoints: %w", err)
	}
	if identifier != opcua.GetEndpointsResponseEncodingID {
		return nil, fmt.Errorf("get endpoints answered with service %d", identifier)
	}
	response, err := decoder.ReadGetEndpointsResponse()
	if err != nil {
		return nil, err
	}
	if response.Header.ServiceResult != opcua.StatusGood {
		return nil, fmt.Errorf("get endpoints returned %s", response.Header.ServiceResult.Hex())
	}
	return response.Endpoints, nil
}

func (c *client) createSession(token opcua.ChannelSecurityToken, endpointURL string) (opcua.NodeID, error) {
	nonce := make([]byte, opcua.MinNonceBytes)
	for index := range nonce {
		nonce[index] = byte(index + 1)
	}
	encoder, err := opcua.NewEncoder(c.limits)
	if err != nil {
		return opcua.NodeID{}, err
	}
	encoder.WriteCreateSessionRequest(opcua.CreateSessionRequest{
		Header:                  requestHeader(opcua.NumericNodeID(0, 0), 3),
		EndpointURL:             endpointURL,
		SessionName:             "opcuaprobe",
		ClientNonce:             nonce,
		RequestedSessionTimeout: 60_000,
	})
	serviceBody, err := encoder.Bytes()
	if err != nil {
		return opcua.NodeID{}, err
	}
	identifier, decoder, err := c.call(token, 3, serviceBody)
	if err != nil {
		return opcua.NodeID{}, fmt.Errorf("create session: %w", err)
	}
	if identifier != opcua.CreateSessionResponseEncodingID {
		return opcua.NodeID{}, fmt.Errorf("create session answered with service %d", identifier)
	}
	response, err := decoder.ReadCreateSessionResponse()
	if err != nil {
		return opcua.NodeID{}, err
	}
	if response.Header.ServiceResult != opcua.StatusGood {
		return opcua.NodeID{}, fmt.Errorf("create session returned %s", response.Header.ServiceResult.Hex())
	}
	if len(response.ServerNonce) < opcua.MinNonceBytes {
		return opcua.NodeID{}, fmt.Errorf("the server nonce was %d bytes", len(response.ServerNonce))
	}
	return response.AuthenticationToken, nil
}

func (c *client) activateSession(token opcua.ChannelSecurityToken, session opcua.NodeID) error {
	encoder, err := opcua.NewEncoder(c.limits)
	if err != nil {
		return err
	}
	encoder.WriteActivateSessionRequest(opcua.ActivateSessionRequest{
		Header:            requestHeader(session, 4),
		UserIdentityToken: opcua.NullExtensionObject(),
	})
	serviceBody, err := encoder.Bytes()
	if err != nil {
		return err
	}
	identifier, decoder, err := c.call(token, 4, serviceBody)
	if err != nil {
		return fmt.Errorf("activate session: %w", err)
	}
	if identifier != opcua.ActivateSessionResponseEncodingID {
		return fmt.Errorf("activate session answered with service %d", identifier)
	}
	response, err := decoder.ReadActivateSessionResponse()
	if err != nil {
		return err
	}
	if response.Header.ServiceResult != opcua.StatusGood {
		return fmt.Errorf("activate session returned %s", response.Header.ServiceResult.Hex())
	}
	return nil
}

func (c *client) browse(token opcua.ChannelSecurityToken, session opcua.NodeID, node opcua.NodeID, handle uint32) ([]opcua.ReferenceDescription, error) {
	encoder, err := opcua.NewEncoder(c.limits)
	if err != nil {
		return nil, err
	}
	encoder.WriteBrowseRequest(opcua.BrowseRequest{
		Header: requestHeader(session, handle),
		NodesToBrowse: []opcua.BrowseDescription{{
			NodeID:          node,
			BrowseDirection: opcua.BrowseDirectionForward,
			ResultMask:      opcua.ResultMaskAll,
		}},
	})
	serviceBody, err := encoder.Bytes()
	if err != nil {
		return nil, err
	}
	identifier, decoder, err := c.call(token, handle, serviceBody)
	if err != nil {
		return nil, fmt.Errorf("browse: %w", err)
	}
	if identifier != opcua.BrowseResponseEncodingID {
		return nil, fmt.Errorf("browse answered with service %d", identifier)
	}
	response, err := decoder.ReadBrowseResponse()
	if err != nil {
		return nil, err
	}
	if len(response.Results) != 1 {
		return nil, fmt.Errorf("browse returned %d results", len(response.Results))
	}
	if response.Results[0].StatusCode != opcua.StatusGood {
		return nil, fmt.Errorf("browse returned %s", response.Results[0].StatusCode.Hex())
	}
	return response.Results[0].References, nil
}

// browseRoot walks Root to Objects to the source folder, which is what a real
// client does rather than assuming a node identifier.
func (c *client) browseRoot(token opcua.ChannelSecurityToken, session opcua.NodeID) (opcua.NodeID, error) {
	objects, err := c.browse(token, session, opcua.NumericNodeID(0, 84), 5)
	if err != nil {
		return opcua.NodeID{}, err
	}
	var objectsFolder opcua.NodeID
	for _, reference := range objects {
		if reference.BrowseName.Name == "Objects" {
			objectsFolder = reference.NodeID.NodeID
		}
	}
	if objectsFolder.IsNull() {
		return opcua.NodeID{}, fmt.Errorf("the Objects folder was not reachable from Root")
	}
	children, err := c.browse(token, session, objectsFolder, 6)
	if err != nil {
		return opcua.NodeID{}, err
	}
	for _, reference := range children {
		if reference.NodeClass == opcua.NodeClassObject && reference.NodeID.NodeID.Namespace != 0 {
			return reference.NodeID.NodeID, nil
		}
	}
	return opcua.NodeID{}, fmt.Errorf("the source folder was not reachable from Objects")
}

// browseForItems descends until it finds variables, since the fixture nests its
// items under a branch.
func (c *client) browseForItems(token opcua.ChannelSecurityToken, session, folder opcua.NodeID) ([]opcua.NodeID, error) {
	handle := uint32(10)
	pending := []opcua.NodeID{folder}
	items := make([]opcua.NodeID, 0, 8)
	for depth := 0; depth < 4 && len(pending) > 0; depth++ {
		next := make([]opcua.NodeID, 0, len(pending))
		for _, node := range pending {
			handle++
			references, err := c.browse(token, session, node, handle)
			if err != nil {
				return nil, err
			}
			for _, reference := range references {
				switch reference.NodeClass {
				case opcua.NodeClassVariable:
					items = append(items, reference.NodeID.NodeID)
				case opcua.NodeClassObject:
					next = append(next, reference.NodeID.NodeID)
				}
			}
		}
		if len(items) > 0 {
			return items, nil
		}
		pending = next
	}
	return items, nil
}

func (c *client) readItem(token opcua.ChannelSecurityToken, session, node opcua.NodeID) error {
	encoder, err := opcua.NewEncoder(c.limits)
	if err != nil {
		return err
	}
	encoder.WriteReadRequest(opcua.ReadRequest{
		Header:             requestHeader(session, 40),
		TimestampsToReturn: opcua.TimestampsBoth,
		NodesToRead: []opcua.ReadValueID{{
			NodeID: node, AttributeID: opcua.AttributeValue,
		}},
	})
	serviceBody, err := encoder.Bytes()
	if err != nil {
		return err
	}
	identifier, decoder, err := c.call(token, 40, serviceBody)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if identifier != opcua.ReadResponseEncodingID {
		return fmt.Errorf("read answered with service %d", identifier)
	}
	response, err := decoder.ReadReadResponse()
	if err != nil {
		return err
	}
	if len(response.Results) != 1 {
		return fmt.Errorf("read returned %d results", len(response.Results))
	}
	result := response.Results[0]
	// The status comes from the source's quality through the Part 8 mapping, so
	// a non-Good status is data rather than a failure. What must hold is that a
	// good status carries a value and a bad one does not.
	if result.Status.IsBad() && !result.Value.IsNull() {
		return fmt.Errorf("a bad status carried a value")
	}
	if !result.Status.IsBad() && result.Value.IsNull() {
		return fmt.Errorf("a usable status carried no value")
	}
	if result.ServerTimestamp.IsZero() {
		return fmt.Errorf("the server timestamp was absent for status %s", result.Status.Hex())
	}
	fmt.Printf("opcua read status=%s sourceTimestampPresent=%t\n",
		result.Status.Hex(), !result.SourceTimestamp.IsZero())
	return nil
}
