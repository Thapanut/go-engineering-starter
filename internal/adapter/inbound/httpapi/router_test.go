package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/platform/auth"
)

// Synthetic test data only.
const (
	issuer   = "test-issuer"
	customer = "cust-test"
)

var secret = []byte("test-secret-at-least-32-bytes-long!!")

// probeModule is a stand-in feature used to exercise the shared HTTP plumbing.
type probeModule struct{}

func (probeModule) Register(v1 fiber.Router) {
	v1.Get("/whoami", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"customerId": customerIDOf(c)}) })
	v1.Post("/echo", func(c *fiber.Ctx) error {
		var body struct {
			Name string `json:"name"`
		}
		if err := decodeStrict(c, &body); err != nil {
			return err
		}
		return c.JSON(body)
	})
	v1.Get("/fail/:kind", func(c *fiber.Ctx) error {
		return map[string]error{
			"validation": domain.Invalid("name is required"),
			"notfound":   fmt.Errorf("thing: %w", domain.ErrNotFound),
			"conflict":   fmt.Errorf("thing: %w", domain.ErrConflict),
			"internal":   errors.New("pq: connection to 10.0.0.5 refused"),
		}[c.Params("kind")]
	})
	v1.Get("/panic", func(*fiber.Ctx) error { panic("boom") })
	v1.Get("/deadline", func(c *fiber.Ctx) error {
		if _, ok := c.UserContext().Deadline(); !ok {
			return errors.New("no deadline on user context")
		}
		return c.SendStatus(http.StatusNoContent)
	})
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type env struct {
	app   *fiber.App
	jwt   *auth.JWT
	logs  *syncBuffer
	ready error
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{jwt: auth.NewJWT(secret, issuer), logs: &syncBuffer{}}
	e.app = NewApp(Deps{
		Auth:           e.jwt,
		Log:            slog.New(slog.NewJSONHandler(e.logs, nil)),
		RequestTimeout: 5 * time.Second,
		Ready:          func(context.Context) error { return e.ready },
	}, probeModule{})
	return e
}

func (e *env) token(t *testing.T) string {
	t.Helper()
	tok, err := e.jwt.Issue(customer, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

type call struct {
	method, path, token, body string
	headers                   map[string]string
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (e *env) do(t *testing.T, c call) response {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), c.method, c.path, strings.NewReader(c.body))
	if c.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	res, err := e.app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{status: res.StatusCode, header: res.Header, body: body}
}

func assertError(t *testing.T, r response, status int, code string) errorBody {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d, want %d; body %s", r.status, status, r.body)
	}
	var e errorBody
	if err := json.Unmarshal(r.body, &e); err != nil {
		t.Fatalf("decode %q: %v", r.body, err)
	}
	if e.Code != code || e.Message == "" || e.TraceID == "" {
		t.Fatalf("error body = %+v, want code %s with message and traceId", e, code)
	}
	if r.header.Get(headerTraceID) != e.TraceID {
		t.Fatalf("X-Trace-Id header %q != body traceId %q", r.header.Get(headerTraceID), e.TraceID)
	}
	return e
}

// unsignedToken builds an "alg":"none" JWT at runtime (no token literal in source).
func unsignedToken(sub string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none","typ":"JWT"}`)) + "." +
		enc([]byte(fmt.Sprintf(`{"sub":%q,"iss":%q,"exp":%d}`, sub, issuer, time.Now().Add(time.Hour).Unix()))) + "."
}

func TestProbes(t *testing.T) {
	e := newEnv(t)
	if r := e.do(t, call{method: "GET", path: "/healthz"}); r.status != http.StatusOK {
		t.Fatalf("healthz = %d", r.status)
	}
	if r := e.do(t, call{method: "GET", path: "/readyz"}); r.status != http.StatusOK {
		t.Fatalf("readyz = %d", r.status)
	}
	e.ready = errors.New("db down")
	assertError(t, e.do(t, call{method: "GET", path: "/readyz"}), http.StatusServiceUnavailable, "NOT_READY")
}

func TestAuthenticatedRouteGetsCustomerID(t *testing.T) {
	e := newEnv(t)
	r := e.do(t, call{method: "GET", path: "/v1/whoami", token: e.token(t)})
	if r.status != http.StatusOK || !strings.Contains(string(r.body), customer) {
		t.Fatalf("status %d body %s", r.status, r.body)
	}
}

func TestUnauthorized(t *testing.T) {
	e := newEnv(t)
	expired, _ := e.jwt.Issue(customer, time.Minute, time.Now().Add(-time.Hour))
	wrongIssuer, _ := auth.NewJWT(secret, "someone-else").Issue(customer, time.Hour, time.Now())
	wrongSecret, _ := auth.NewJWT([]byte("another-secret-that-is-32-bytes-long"), issuer).Issue(customer, time.Hour, time.Now())
	for name, tok := range map[string]string{
		"no token": "", "garbage": "abc.def.ghi", "expired": expired,
		"wrong issuer": wrongIssuer, "wrong secret": wrongSecret, "alg none": unsignedToken(customer),
	} {
		t.Run(name, func(t *testing.T) {
			assertError(t, e.do(t, call{method: "GET", path: "/v1/whoami", token: tok}), http.StatusUnauthorized, "UNAUTHORIZED")
		})
	}
}

func TestDomainErrorMapping(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	for kind, want := range map[string]struct {
		status int
		code   string
	}{
		"validation": {http.StatusBadRequest, "VALIDATION_ERROR"},
		"notfound":   {http.StatusNotFound, "NOT_FOUND"},
		"conflict":   {http.StatusConflict, "CONFLICT"},
		"internal":   {http.StatusInternalServerError, "INTERNAL_ERROR"},
	} {
		t.Run(kind, func(t *testing.T) {
			body := assertError(t, e.do(t, call{method: "GET", path: "/v1/fail/" + kind, token: tok}), want.status, want.code)
			if strings.Contains(body.Message, "10.0.0.5") {
				t.Fatalf("internal detail leaked: %q", body.Message)
			}
		})
	}
}

func TestPanicIsRecovered(t *testing.T) {
	e := newEnv(t)
	assertError(t, e.do(t, call{method: "GET", path: "/v1/panic", token: e.token(t)}), http.StatusInternalServerError, "INTERNAL_ERROR")
}

func TestRequestContextHasDeadline(t *testing.T) {
	e := newEnv(t)
	if r := e.do(t, call{method: "GET", path: "/v1/deadline", token: e.token(t)}); r.status != http.StatusNoContent {
		t.Fatalf("status %d body %s", r.status, r.body)
	}
}

func TestStrictJSONDecoding(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	if r := e.do(t, call{method: "POST", path: "/v1/echo", token: tok, body: `{"name":"x"}`}); r.status != http.StatusOK {
		t.Fatalf("valid body: status %d", r.status)
	}
	for name, body := range map[string]string{
		"unknown field": `{"name":"x","isAdmin":true}`,
		"wrong type":    `{"name":1}`,
		"malformed":     `{"name":`,
		"trailing data": `{"name":"x"}{}`,
	} {
		t.Run(name, func(t *testing.T) {
			assertError(t, e.do(t, call{method: "POST", path: "/v1/echo", token: tok, body: body}), http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
}

// BodyLimit is enforced by fasthttp while reading the request, which app.Test
// cannot observe, so this test goes through a real listener.
func TestBodyTooLarge(t *testing.T) {
	e := newEnv(t)
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = e.app.Listener(ln) }()
	t.Cleanup(func() { _ = e.app.Shutdown() })

	big := `{"name":"` + strings.Repeat("x", maxBodyBytes) + `"}`
	req, _ := http.NewRequestWithContext(t.Context(), "POST", "http://"+ln.Addr().String()+"/v1/echo", strings.NewReader(big))
	req.Header.Set("Authorization", "Bearer "+e.token(t))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	assertError(t, response{status: res.StatusCode, header: res.Header, body: body}, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE")
}

func TestTraceIDPropagation(t *testing.T) {
	e := newEnv(t)
	r := e.do(t, call{method: "GET", path: "/healthz", headers: map[string]string{headerTraceID: "client-trace-12345"}})
	if got := r.header.Get(headerTraceID); got != "client-trace-12345" {
		t.Fatalf("trace id = %q, want client value", got)
	}
	// Malformed incoming ids are replaced, preventing log injection.
	r = e.do(t, call{method: "GET", path: "/healthz", headers: map[string]string{headerTraceID: "bad value!"}})
	if got := r.header.Get(headerTraceID); got == "" || strings.ContainsAny(got, " !") {
		t.Fatalf("trace id = %q, want generated", got)
	}
}

func TestAccessLogHasNoSensitiveData(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.do(t, call{method: "POST", path: "/v1/echo", token: tok, body: `{"name":"secret-name"}`})
	e.do(t, call{method: "GET", path: "/v1/fail/notfound", token: tok})
	logs := e.logs.String()
	for _, forbidden := range []string{tok, "secret-name", customer} {
		if strings.Contains(logs, forbidden) {
			t.Errorf("log contains sensitive value %q:\n%s", forbidden, logs)
		}
	}
	for _, want := range []string{`"route":"/v1/echo"`, `"route":"/v1/fail/:kind"`, `"status":404`, `"trace_id"`, `"latency_ms"`, `"status":200`} {
		if !strings.Contains(logs, want) {
			t.Errorf("log missing %s:\n%s", want, logs)
		}
	}
}

func TestUnknownRouteUsesErrorSchema(t *testing.T) {
	e := newEnv(t)
	assertError(t, e.do(t, call{method: "GET", path: "/nope"}), http.StatusNotFound, "NOT_FOUND")
	if !strings.Contains(e.logs.String(), `"route":"unmatched"`) {
		t.Errorf("unmatched route not logged as such:\n%s", e.logs.String())
	}
}
