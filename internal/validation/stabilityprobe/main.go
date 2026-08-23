// Command stabilityprobe exercises the HTTP boundary against a real OPC DA
// validation run. It is test tooling only and never logs response bodies or
// process values.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type options struct {
	baseURL          string
	rapidRequests    int
	workers          int
	requestsPerWork  int
	overloadRequests int
	slowConnections  int
	headerTimeout    time.Duration
}

type probe struct {
	baseURL *url.URL
	client  *http.Client
}

type errorResponse struct {
	Error struct {
		Layer string `json:"layer"`
		Code  string `json:"code"`
	} `json:"error"`
}

type statusResponse struct {
	State        string `json:"state"`
	WriteEnabled bool   `json:"writeEnabled"`
	Source       struct {
		ConnectionGeneration uint64 `json:"connectionGeneration"`
	} `json:"source"`
	Frontend struct {
		HTTP struct {
			Listening bool `json:"listening"`
		} `json:"http"`
	} `json:"frontend"`
}

type readResponse struct {
	Results []struct {
		ItemID            string          `json:"itemId"`
		OK                bool            `json:"ok"`
		DataType          typeInfo        `json:"dataType"`
		CanonicalDataType typeInfo        `json:"canonicalDataType"`
		Quality           *uint16         `json:"quality"`
		Timestamp         json.RawMessage `json:"timestamp"`
		TimestampPresent  bool            `json:"timestampPresent"`
		HRESULT           struct {
			Value int32  `json:"value"`
			Hex   string `json:"hex"`
		} `json:"hresult"`
	} `json:"results"`
}

type typeInfo struct {
	Code uint16 `json:"code"`
	Name string `json:"name"`
}

type browseResponse struct {
	Path    []string `json:"path"`
	Entries []struct {
		Kind   string  `json:"kind"`
		ItemID *string `json:"itemId"`
	} `json:"entries"`
}

func main() {
	var configured options
	flag.StringVar(&configured.baseURL, "base-url", "http://127.0.0.1:18080", "adapter base URL")
	flag.IntVar(&configured.rapidRequests, "rapid-requests", 2000, "sequential no-delay Read requests")
	flag.IntVar(&configured.workers, "workers", 16, "mixed concurrent workers")
	flag.IntVar(&configured.requestsPerWork, "requests-per-worker", 100, "requests per mixed worker")
	flag.IntVar(&configured.overloadRequests, "overload-requests", 48, "simultaneous maximum-size Reads")
	flag.IntVar(&configured.slowConnections, "slow-connections", 48, "incomplete-header connections")
	flag.DurationVar(&configured.headerTimeout, "header-timeout", 5*time.Second, "configured adapter header timeout")
	flag.Parse()

	if err := validateOptions(configured); err != nil {
		fmt.Fprintln(os.Stderr, "invalid stability probe options:", err)
		os.Exit(2)
	}
	baseURL, err := url.Parse(configured.baseURL)
	if err != nil || baseURL.Scheme != "http" || baseURL.Host == "" {
		fmt.Fprintln(os.Stderr, "invalid HTTP base URL")
		os.Exit(2)
	}
	transport := &http.Transport{
		MaxIdleConns:        128,
		MaxIdleConnsPerHost: 64,
		MaxConnsPerHost:     64,
		IdleConnTimeout:     20 * time.Second,
	}
	p := probe{baseURL: baseURL, client: &http.Client{Transport: transport, Timeout: 20 * time.Second}}
	defer transport.CloseIdleConnections()

	started := time.Now()
	steps := []struct {
		name string
		run  func() error
	}{
		{name: "normal semantics", run: p.normal},
		{name: "invalid and anomalous requests", run: p.anomalies},
		{name: "slow and oversized headers", run: func() error { return p.slowHeaders(configured.slowConnections, configured.headerTimeout) }},
		{name: "rapid sequential requests", run: func() error { return p.rapid(configured.rapidRequests) }},
		{name: "mixed concurrent requests", run: func() error { return p.concurrent(configured.workers, configured.requestsPerWork) }},
		{name: "bounded overload and recovery", run: func() error { return p.overload(configured.overloadRequests) }},
	}
	for _, step := range steps {
		stepStarted := time.Now()
		if err := step.run(); err != nil {
			fmt.Fprintf(os.Stderr, "STABILITY_STEP_FAIL name=%q error=%q\n", step.name, err)
			os.Exit(1)
		}
		fmt.Printf("STABILITY_STEP_PASS name=%q duration=%s\n", step.name, time.Since(stepStarted).Round(time.Millisecond))
	}
	fmt.Printf("HTTP_STABILITY_PASS rapid=%d mixed=%d overload=%d slowConnections=%d duration=%s\n",
		configured.rapidRequests, configured.workers*configured.requestsPerWork,
		configured.overloadRequests, configured.slowConnections, time.Since(started).Round(time.Millisecond))
}

func validateOptions(configured options) error {
	if configured.rapidRequests < 1 || configured.rapidRequests > 100000 ||
		configured.workers < 1 || configured.workers > 31 ||
		configured.requestsPerWork < 1 || configured.requestsPerWork > 10000 ||
		configured.overloadRequests < 33 || configured.overloadRequests > 63 ||
		configured.slowConnections < 1 || configured.slowConnections > 63 ||
		configured.headerTimeout < time.Second || configured.headerTimeout > time.Minute {
		return errors.New("one or more values are outside their safe validation bounds")
	}
	return nil
}

func (p probe) request(method, path string, body []byte, headers map[string]string) (int, []byte, error) {
	request, err := http.NewRequest(method, p.baseURL.ResolveReference(&url.URL{Path: path}).String(), bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 3<<20))
	return response.StatusCode, responseBody, err
}

func (p probe) connectedStatus() error {
	status, body, err := p.request(http.MethodGet, "/v1/status", nil, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("status endpoint returned HTTP %d", status)
	}
	var decoded statusResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return fmt.Errorf("decode status: %w", err)
	}
	if decoded.State != "connected" || decoded.Source.ConnectionGeneration < 1 || !decoded.Frontend.HTTP.Listening {
		return fmt.Errorf("status did not report a listening connected runtime")
	}
	return nil
}

func (p probe) normal() error {
	if err := p.connectedStatus(); err != nil {
		return err
	}
	if err := p.validatePartialRead(); err != nil {
		return err
	}
	status, body, err := p.request(http.MethodPost, "/v1/browse", []byte(`{"path":["Test"],"filter":"all"}`), nil)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("nested Browse failed: status=%d error=%v", status, err)
	}
	var decoded browseResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	if len(decoded.Path) != 1 || decoded.Path[0] != "Test" || len(decoded.Entries) != 3 {
		return errors.New("nested Browse identity or count changed")
	}
	return nil
}

func (p probe) validatePartialRead() error {
	status, body, err := p.request(http.MethodPost, "/v1/read", []byte(`{"source":"device","items":[{"itemId":"Test/Int32"},{"itemId":"__stability_invalid__"}]}`), nil)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("partial Read failed: status=%d error=%v", status, err)
	}
	var decoded readResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	if len(decoded.Results) != 2 || decoded.Results[0].ItemID != "Test/Int32" || !decoded.Results[0].OK ||
		decoded.Results[1].ItemID != "__stability_invalid__" || decoded.Results[1].OK {
		return errors.New("partial Read ordering or success semantics changed")
	}
	known := decoded.Results[0]
	if known.DataType.Code != 3 || known.DataType.Name != "VT_I4" || known.CanonicalDataType.Code != 3 ||
		known.CanonicalDataType.Name != "VT_I4" || known.Quality == nil || *known.Quality != 192 ||
		known.HRESULT.Value != 0 || known.HRESULT.Hex != "0x00000000" {
		return errors.New("Read VARTYPE, raw Quality, or HRESULT metadata changed")
	}
	timestampNull := bytes.Equal(bytes.TrimSpace(known.Timestamp), []byte("null"))
	if known.TimestampPresent == timestampNull {
		return errors.New("timestamp presence contradicted timestamp representation")
	}
	unknown := decoded.Results[1]
	if unknown.HRESULT.Value >= 0 || unknown.HRESULT.Hex != "0xC0040007" {
		return errors.New("invalid ItemID HRESULT changed")
	}
	return nil
}

func (p probe) expectError(name, method, path string, body []byte, expectedStatus int, layer, code string) error {
	status, responseBody, err := p.request(method, path, body, nil)
	if err != nil {
		return fmt.Errorf("%s transport: %w", name, err)
	}
	if status != expectedStatus {
		return fmt.Errorf("%s returned HTTP %d, expected %d", name, status, expectedStatus)
	}
	var decoded errorResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return fmt.Errorf("%s error JSON: %w", name, err)
	}
	if decoded.Error.Layer != layer || decoded.Error.Code != code {
		return fmt.Errorf("%s returned error layer/code %s/%s", name, decoded.Error.Layer, decoded.Error.Code)
	}
	return nil
}

func (p probe) anomalies() error {
	checks := []struct {
		name, method, path, body, layer, code string
		status                                int
	}{
		{name: "malformed JSON", method: http.MethodPost, path: "/v1/read", body: `{"source":`, status: 400, layer: "frontend", code: "INVALID_REQUEST"},
		{name: "trailing JSON", method: http.MethodPost, path: "/v1/read", body: `{"items":[]} {}`, status: 400, layer: "frontend", code: "INVALID_REQUEST"},
		{name: "unknown field", method: http.MethodPost, path: "/v1/read", body: `{"items":[],"unknown":true}`, status: 400, layer: "frontend", code: "INVALID_REQUEST"},
		{name: "unpaired surrogate", method: http.MethodPost, path: "/v1/read", body: `{"items":[{"itemId":"\uD800"}]}`, status: 400, layer: "frontend", code: "INVALID_REQUEST"},
		{name: "NUL ItemID", method: http.MethodPost, path: "/v1/read", body: `{"items":[{"itemId":"a\u0000b"}]}`, status: 400, layer: "frontend", code: "INVALID_REQUEST"},
		{name: "empty batch", method: http.MethodPost, path: "/v1/read", body: `{"items":[]}`, status: 400, layer: "frontend", code: "INVALID_REQUEST"},
		{name: "wrong source", method: http.MethodPost, path: "/v1/read", body: `{"source":"cache","items":[{"itemId":"Test/Int32"}]}`, status: 400, layer: "frontend", code: "INVALID_REQUEST"},
		{name: "invalid browse filter", method: http.MethodPost, path: "/v1/browse", body: `{"path":[],"filter":"wildcard"}`, status: 400, layer: "frontend", code: "INVALID_REQUEST"},
		{name: "wrong method", method: http.MethodGet, path: "/v1/read", status: 404, layer: "frontend", code: "NOT_FOUND"},
		{name: "unknown endpoint", method: http.MethodGet, path: "/v1/not-present", status: 404, layer: "frontend", code: "NOT_FOUND"},
		{name: "path traversal", method: http.MethodGet, path: "/v1/../status", status: 404, layer: "frontend", code: "NOT_FOUND"},
	}
	for _, check := range checks {
		if err := p.expectError(check.name, check.method, check.path, []byte(check.body), check.status, check.layer, check.code); err != nil {
			return err
		}
	}

	invalidUTF8 := []byte(`{"items":[{"itemId":"`)
	invalidUTF8 = append(invalidUTF8, 0xff)
	invalidUTF8 = append(invalidUTF8, []byte(`"}]}`)...)
	if err := p.expectError("invalid UTF-8", http.MethodPost, "/v1/read", invalidUTF8, 400, "frontend", "INVALID_REQUEST"); err != nil {
		return err
	}

	items := make([]string, 101)
	for index := range items {
		items[index] = `{"itemId":"Test/Int32"}`
	}
	tooMany := []byte(`{"source":"device","items":[` + strings.Join(items, ",") + `]}`)
	if err := p.expectError("oversized batch", http.MethodPost, "/v1/read", tooMany, 400, "frontend", "REQUEST_LIMIT_EXCEEDED"); err != nil {
		return err
	}
	longItemID := []byte(`{"items":[{"itemId":"` + strings.Repeat("x", 1025) + `"}]}`)
	if err := p.expectError("long ItemID", http.MethodPost, "/v1/read", longItemID, 400, "frontend", "ITEM_ID_TOO_LONG"); err != nil {
		return err
	}
	oversizedBody := []byte(`{"items":[{"itemId":"` + strings.Repeat("x", (1<<20)+1) + `"}]}`)
	if err := p.expectError("oversized body", http.MethodPost, "/v1/read", oversizedBody, 413, "frontend", "REQUEST_BODY_TOO_LARGE"); err != nil {
		return err
	}
	path := make([]string, 65)
	for index := range path {
		path[index] = `"x"`
	}
	deepBrowse := []byte(`{"path":[` + strings.Join(path, ",") + `],"filter":"all"}`)
	return p.expectError("deep Browse", http.MethodPost, "/v1/browse", deepBrowse, 400, "frontend", "REQUEST_LIMIT_EXCEEDED")
}

func (p probe) slowHeaders(count int, headerTimeout time.Duration) error {
	status, _, err := p.request(http.MethodGet, "/v1/status", nil, map[string]string{"X-Stability-Large": strings.Repeat("x", 64<<10)})
	if err != nil {
		return fmt.Errorf("large header transport: %w", err)
	}
	if status != http.StatusRequestHeaderFieldsTooLarge {
		return fmt.Errorf("large header returned HTTP %d, expected 431", status)
	}

	address := p.baseURL.Host
	connections := make([]net.Conn, 0, count)
	for index := 0; index < count; index++ {
		connection, err := net.DialTimeout("tcp", address, 2*time.Second)
		if err != nil {
			closeConnections(connections)
			return fmt.Errorf("open incomplete-header connection %d: %w", index, err)
		}
		if _, err := io.WriteString(connection, "GET /v1/status HTTP/1.1\r\nHost: "+address+"\r\nX-Incomplete: "); err != nil {
			_ = connection.Close()
			closeConnections(connections)
			return err
		}
		connections = append(connections, connection)
	}
	if err := p.connectedStatus(); err != nil {
		closeConnections(connections)
		return fmt.Errorf("normal status starved by incomplete headers: %w", err)
	}
	time.Sleep(headerTimeout + time.Second)
	for index, connection := range connections {
		_ = connection.SetReadDeadline(time.Now().Add(time.Second))
		_, readErr := io.Copy(io.Discard, connection)
		var networkError net.Error
		if errors.As(readErr, &networkError) && networkError.Timeout() {
			closeConnections(connections)
			return fmt.Errorf("incomplete-header connection %d remained open past timeout", index)
		}
	}
	closeConnections(connections)
	return p.connectedStatus()
}

func closeConnections(connections []net.Conn) {
	for _, connection := range connections {
		_ = connection.Close()
	}
}

func (p probe) rapid(count int) error {
	body := []byte(`{"source":"device","items":[{"itemId":"Test/Int32"}]}`)
	for index := 0; index < count; index++ {
		status, responseBody, err := p.request(http.MethodPost, "/v1/read", body, nil)
		if err != nil || status != http.StatusOK {
			return fmt.Errorf("rapid Read %d: status=%d error=%v", index, status, err)
		}
		var decoded readResponse
		if err := json.Unmarshal(responseBody, &decoded); err != nil || len(decoded.Results) != 1 || !decoded.Results[0].OK {
			return fmt.Errorf("rapid Read %d response validation failed", index)
		}
	}
	return nil
}

func (p probe) concurrent(workers, each int) error {
	var wait sync.WaitGroup
	errorsFound := make(chan error, workers)
	start := make(chan struct{})
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			<-start
			for requestIndex := 0; requestIndex < each; requestIndex++ {
				var err error
				switch (worker + requestIndex) % 3 {
				case 0:
					err = p.connectedStatus()
				case 1:
					err = p.validatePartialRead()
				default:
					status, _, requestErr := p.request(http.MethodPost, "/v1/browse", []byte(`{"path":["Test"],"filter":"item"}`), nil)
					if requestErr != nil || status != http.StatusOK {
						err = fmt.Errorf("Browse status=%d error=%v", status, requestErr)
					}
				}
				if err != nil {
					errorsFound <- fmt.Errorf("worker=%d request=%d: %w", worker, requestIndex, err)
					return
				}
			}
		}(worker)
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		return err
	}
	return nil
}

func (p probe) overload(count int) error {
	items := make([]string, 100)
	for index := range items {
		items[index] = `{"itemId":"Test/Int32"}`
	}
	body := []byte(`{"source":"device","items":[` + strings.Join(items, ",") + `]}`)
	start := make(chan struct{})
	var wait sync.WaitGroup
	var successes atomic.Int64
	var rejected atomic.Int64
	errorsFound := make(chan error, count)
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			status, responseBody, err := p.request(http.MethodPost, "/v1/read", body, nil)
			if err != nil {
				errorsFound <- fmt.Errorf("overload request %d transport: %w", index, err)
				return
			}
			switch status {
			case http.StatusOK:
				successes.Add(1)
			case http.StatusServiceUnavailable:
				var decoded errorResponse
				if json.Unmarshal(responseBody, &decoded) != nil || decoded.Error.Layer != "frontend" || decoded.Error.Code != "QUEUE_FULL" {
					errorsFound <- fmt.Errorf("overload request %d returned unexpected 503", index)
					return
				}
				rejected.Add(1)
			default:
				errorsFound <- fmt.Errorf("overload request %d returned HTTP %d", index, status)
			}
		}(index)
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		return err
	}
	if successes.Load() == 0 || rejected.Load() == 0 || successes.Load()+rejected.Load() != int64(count) {
		return fmt.Errorf("backpressure was not observable: success=%d queueFull=%d", successes.Load(), rejected.Load())
	}
	fmt.Printf("STABILITY_BACKPRESSURE success=%d queueFull=%d\n", successes.Load(), rejected.Load())
	if err := p.connectedStatus(); err != nil {
		return fmt.Errorf("status did not recover after overload: %w", err)
	}
	return p.rapid(10)
}
