package tkhttp

import (
	"context"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itrookie-sky/whtlib/adapter"
)

type roundTripFunc func(request *http.Request) (response *http.Response, err error)

func (f roundTripFunc) RoundTrip(request *http.Request) (response *http.Response, err error) {
	return f(request)
}

func nativeResponse(status int, body string) (response *http.Response) {
	return &http.Response{StatusCode: status, Header: http.Header{"X-Response": {"original"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func mustClient(t *testing.T, config Config, opts ...Option) (client *Client) {
	t.Helper()
	client, err := New(config, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	return
}

func quietConfig() (config Config) {
	return Config{BaseURL: "https://api.example.com/v1/", DisableLog: true}
}

type logRecord struct {
	fields map[string]any
	start  time.Time
	skip   int
	level  string
	ctx    context.Context
}

type logSink struct {
	mu       sync.Mutex
	records  []logRecord
	clones   atomic.Int32
	starts   atomic.Int32
	failAt   int32
	typedNil bool
}

type recordingAcc struct {
	mu     sync.Mutex
	sink   *logSink
	fields map[string]any
	start  time.Time
	skip   int
}

func newRecordingAcc() (acc *recordingAcc) {
	return &recordingAcc{sink: &logSink{}, fields: make(map[string]any)}
}

func (a *recordingAcc) Clone() (acc adapter.LogAcc) {
	if a.sink.clones.Add(1) == a.sink.failAt {
		if a.sink.typedNil {
			return (*recordingAcc)(nil)
		}
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return &recordingAcc{sink: a.sink, fields: maps.Clone(a.fields), start: a.start, skip: a.skip}
}

func (a *recordingAcc) SetStartTime(start time.Time) (acc adapter.LogAcc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.start = start
	a.sink.starts.Add(1)
	return a
}

func (a *recordingAcc) SetValue(key string, value any) (acc adapter.LogAcc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fields[key] = value
	return a
}

func (a *recordingAcc) Update(values map[string]any) (acc adapter.LogAcc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	maps.Copy(a.fields, values)
	return a
}

func (a *recordingAcc) Skip(n int) (acc adapter.LogAcc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.skip += n
	return a
}

func (a *recordingAcc) SetErr(value *error) (acc adapter.LogAcc) {
	return a
}

func (a *recordingAcc) emit(ctx context.Context, level string) {
	a.mu.Lock()
	record := logRecord{fields: maps.Clone(a.fields), start: a.start, skip: a.skip, level: level, ctx: ctx}
	a.mu.Unlock()
	a.sink.mu.Lock()
	a.sink.records = append(a.sink.records, record)
	a.sink.mu.Unlock()
}

func (a *recordingAcc) Debug(ctx context.Context, message string, args ...any) {
	a.emit(ctx, "debug")
}

func (a *recordingAcc) Info(ctx context.Context, message string, args ...any) {
	a.emit(ctx, "info")
}

func (a *recordingAcc) Warn(ctx context.Context, message string, args ...any) {
	a.emit(ctx, "warn")
}

func (a *recordingAcc) Error(ctx context.Context, message string, args ...any) {
	a.emit(ctx, "error")
}

func TestNewValidationAndDefaults(t *testing.T) {
	badClone := newRecordingAcc()
	badClone.sink.failAt = 1
	typedClone := newRecordingAcc()
	typedClone.sink.failAt = 1
	typedClone.sink.typedNil = true
	var nilTransport *http.Transport
	var nilAcc *recordingAcc
	cases := []struct {
		name   string
		config Config
		opts   []Option
	}{
		{name: "missing URL"},
		{name: "relative URL", config: Config{BaseURL: "/path", DisableLog: true}},
		{name: "userinfo", config: Config{BaseURL: "https://secret@example.com", DisableLog: true}},
		{name: "query", config: Config{BaseURL: "https://example.com?", DisableLog: true}},
		{name: "fragment", config: Config{BaseURL: "https://example.com/#", DisableLog: true}},
		{name: "port", config: Config{BaseURL: "http://example.com:99999", DisableLog: true}},
		{name: "scheme", config: Config{BaseURL: "ftp://example.com", DisableLog: true}},
		{name: "timeout", config: Config{BaseURL: "https://example.com", Timeout: -time.Second, DisableLog: true}},
		{name: "limit", config: Config{BaseURL: "https://example.com", MaxResponseBytes: -1, DisableLog: true}},
		{name: "overflow", config: Config{BaseURL: "https://example.com", MaxResponseBytes: 1<<63 - 1, DisableLog: true}},
		{name: "header", config: Config{BaseURL: "https://example.com", DefaultHeader: http.Header{"X-Test": {"secret\nvalue"}}, DisableLog: true}},
		{name: "nil client", config: quietConfig(), opts: []Option{WithHTTPClient(nil)}},
		{name: "nil pool", config: quietConfig(), opts: []Option{WithTransport(nil)}},
		{name: "typed nil transport", config: quietConfig(), opts: []Option{WithHTTPClient(&http.Client{Transport: nilTransport})}},
		{name: "missing logger", config: Config{BaseURL: "https://example.com"}},
		{name: "nil logger", config: quietConfig(), opts: []Option{WithLogAcc(nil)}},
		{name: "typed nil logger", config: quietConfig(), opts: []Option{WithLogAcc(nilAcc)}},
		{name: "nil initial clone", config: Config{BaseURL: "https://example.com"}, opts: []Option{WithLogAcc(badClone)}},
		{name: "typed nil initial clone", config: Config{BaseURL: "https://example.com"}, opts: []Option{WithLogAcc(typedClone)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, err := New(tc.config, tc.opts...)
			if client != nil || !errors.Is(err, ErrInvalidConfig) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("client=%v err=%v", client, err)
			}
		})
	}
	acc := newRecordingAcc()
	client := mustClient(t, quietConfig(), nil, WithLogAcc(acc))
	second := mustClient(t, quietConfig())
	if client.config.Timeout != 10*time.Second || client.config.MaxResponseBytes != 8<<20 || client.ownedPool == nil || client.httpClient.Transport != client.ownedPool || client.ownedPool == second.ownedPool {
		t.Fatal("invalid defaults or shared default pool")
	}
	pool := client.ownedPool
	if pool.MaxIdleConns != 100 || pool.MaxIdleConnsPerHost != 0 || pool.MaxConnsPerHost != 0 || !pool.ForceAttemptHTTP2 || pool.IdleConnTimeout != 90*time.Second || pool.TLSHandshakeTimeout != 10*time.Second || pool.ExpectContinueTimeout != time.Second || pool.Proxy == nil || pool.DialContext == nil {
		t.Fatal("default pool differs from documented baseline")
	}
	if acc.sink.clones.Load() != 0 || acc.sink.starts.Load() != 0 {
		t.Fatal("disabled logging touched accumulator")
	}
}

func TestTransportPriorityAndBorrowing(t *testing.T) {
	pool := &http.Transport{}
	other := &http.Transport{}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	native := &http.Client{Transport: other, Jar: jar, Timeout: time.Second}
	for _, opts := range [][]Option{
		{WithHTTPClient(native), WithTransport(pool)},
		{WithTransport(pool), WithHTTPClient(native)},
		{WithTransport(other), WithTransport(pool)},
	} {
		client := mustClient(t, quietConfig(), opts...)
		if client.httpClient.Transport != pool || client.ownedPool != nil {
			t.Fatal("pool priority or identity changed")
		}
	}
	borrowed := mustClient(t, quietConfig(), WithHTTPClient(native))
	if borrowed.httpClient == native || borrowed.httpClient.Transport != other || borrowed.httpClient.Jar != jar || borrowed.httpClient.Timeout != time.Second || native.Transport != other || native.CheckRedirect != nil {
		t.Fatal("borrowed client was changed or not copied correctly")
	}
	empty := mustClient(t, quietConfig(), WithHTTPClient(&http.Client{}))
	if empty.ownedPool == nil {
		t.Fatal("nil client transport must create a default pool")
	}
	var typedNil *http.Transport
	_ = mustClient(t, quietConfig(), WithHTTPClient(&http.Client{Transport: typedNil}), WithTransport(pool))
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
		t.Error("global transport must not be used")
		return nil, errors.New("global")
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	independent := mustClient(t, quietConfig())
	if independent.ownedPool == nil {
		t.Fatal("default pool depends on replaced global transport")
	}
}

func TestPoolReuseOwnershipAndRedirect(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/redirect" {
			writer.Header().Set("Location", "/final")
			writer.WriteHeader(http.StatusFound)
			_, _ = io.WriteString(writer, "redirect")
			return
		}
		http.SetCookie(writer, &http.Cookie{Name: "session", Value: "saved"})
		_, _ = io.WriteString(writer, request.Header.Get("Cookie"))
	}))
	server.Config.ConnState = func(conn net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	config := Config{BaseURL: server.URL, DisableLog: true}
	pool := &http.Transport{}
	t.Cleanup(pool.CloseIdleConnections)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	var redirects atomic.Int32
	native := &http.Client{Transport: pool, Jar: jar, CheckRedirect: func(request *http.Request, via []*http.Request) (err error) {
		redirects.Add(1)
		return nil
	}}
	first := mustClient(t, config, WithTransport(pool))
	second := mustClient(t, config, WithHTTPClient(native))
	_, err = first.Get(t.Context(), "/")
	if err != nil {
		t.Fatal(err)
	}
	first.CloseIdleConnections()
	_, err = second.Get(t.Context(), "/")
	if err != nil {
		t.Fatal(err)
	}
	response, err := second.Get(t.Context(), "/")
	if err != nil || !strings.Contains(string(response.Body), "session=saved") || connections.Load() != 1 {
		t.Fatalf("pool or jar not shared: connections=%d err=%v", connections.Load(), err)
	}
	response, err = second.Get(t.Context(), "/redirect")
	status, ok := errors.AsType[*StatusError](err)
	if !ok || status.StatusCode != 302 || string(response.Body) != "redirect" || redirects.Load() != 0 {
		t.Fatalf("redirect was followed: %v %v", response, err)
	}
	owned := mustClient(t, config)
	_, err = owned.Get(t.Context(), "/")
	if err != nil {
		t.Fatal(err)
	}
	before := connections.Load()
	owned.CloseIdleConnections()
	_, err = owned.Get(t.Context(), "/")
	if err != nil || connections.Load() != before+1 {
		t.Fatalf("owned idle pool was not closed: %d %v", connections.Load(), err)
	}
}

func TestInvalidClientDoesNotPanic(t *testing.T) {
	for _, client := range []*Client{nil, {}} {
		response, err := client.Get(t.Context(), "anything")
		if response != nil || !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("response=%v err=%v", response, err)
		}
		client.CloseIdleConnections()
	}
}
