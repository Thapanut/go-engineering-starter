package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

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

func (probeModule) Register(v1 *gin.RouterGroup) {
	v1.GET("/whoami", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"customerId": customerIDOf(c)}) })
	v1.POST("/echo", func(c *gin.Context) {
		var body struct {
			Name string `json:"name"`
		}
		if err := decodeStrict(c.Request.Body, &body); err != nil {
			writeError(c, slog.Default(), err)
			return
		}
		c.JSON(http.StatusOK, body)
	})
	v1.GET("/fail/:kind", func(c *gin.Context) {
		errs := map[string]error{
			"validation": domain.Invalid("name is required"),
			"notfound":   fmt.Errorf("thing: %w", domain.ErrNotFound),
			"conflict":   fmt.Errorf("thing: %w", domain.ErrConflict),
			"internal":   errors.New("pq: connection to 10.0.0.5 refused"),
		}
		writeError(c, slog.Default(), errs[c.Param("kind")])
	})
	v1.GET("/panic", func(*gin.Context) { panic("boom") })
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
	h     http.Handler
	jwt   *auth.JWT
	logs  *syncBuffer
	ready error
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{jwt: auth.NewJWT(secret, issuer), logs: &syncBuffer{}}
	e.h = NewRouter(Deps{
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

func (e *env) do(t *testing.T, c call) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), c.method, c.path, strings.NewReader(c.body))
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func assertError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) errorBody {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body.String())
	}
	var e errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if e.Code != code || e.Message == "" || e.TraceID == "" {
		t.Fatalf("error body = %+v, want code %s with message and traceId", e, code)
	}
	if rec.Header().Get(headerTraceID) != e.TraceID {
		t.Fatalf("X-Trace-Id header %q != body traceId %q", rec.Header().Get(headerTraceID), e.TraceID)
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
	if rec := e.do(t, call{method: "GET", path: "/healthz"}); rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d", rec.Code)
	}
	if rec := e.do(t, call{method: "GET", path: "/readyz"}); rec.Code != http.StatusOK {
		t.Fatalf("readyz = %d", rec.Code)
	}
	e.ready = errors.New("db down")
	assertError(t, e.do(t, call{method: "GET", path: "/readyz"}), http.StatusServiceUnavailable, "NOT_READY")
}

func TestAuthenticatedRouteGetsCustomerID(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, call{method: "GET", path: "/v1/whoami", token: e.token(t)})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), customer) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
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

func TestStrictJSONDecoding(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	if rec := e.do(t, call{method: "POST", path: "/v1/echo", token: tok, body: `{"name":"x"}`}); rec.Code != http.StatusOK {
		t.Fatalf("valid body: status %d", rec.Code)
	}
	for name, body := range map[string]string{
		"unknown field":  `{"name":"x","isAdmin":true}`,
		"wrong type":     `{"name":1}`,
		"malformed":      `{"name":`,
		"trailing data":  `{"name":"x"}{}`,
		"body too large": `{"name":"` + strings.Repeat("x", maxBodyBytes) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			assertError(t, e.do(t, call{method: "POST", path: "/v1/echo", token: tok, body: body}), http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
}

func TestTraceIDPropagation(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, call{method: "GET", path: "/healthz", headers: map[string]string{headerTraceID: "client-trace-12345"}})
	if got := rec.Header().Get(headerTraceID); got != "client-trace-12345" {
		t.Fatalf("trace id = %q, want client value", got)
	}
	// Malformed incoming ids are replaced, preventing log injection.
	rec = e.do(t, call{method: "GET", path: "/healthz", headers: map[string]string{headerTraceID: "bad\nvalue"}})
	if got := rec.Header().Get(headerTraceID); got == "" || strings.ContainsAny(got, "\n ") {
		t.Fatalf("trace id = %q, want generated", got)
	}
}

func TestAccessLogHasNoSensitiveData(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.do(t, call{method: "POST", path: "/v1/echo", token: tok, body: `{"name":"secret-name"}`})
	logs := e.logs.String()
	for _, forbidden := range []string{tok, "secret-name", customer} {
		if strings.Contains(logs, forbidden) {
			t.Errorf("log contains sensitive value %q:\n%s", forbidden, logs)
		}
	}
	for _, want := range []string{`"route":"/v1/echo"`, `"trace_id"`, `"latency_ms"`, `"status":200`} {
		if !strings.Contains(logs, want) {
			t.Errorf("log missing %s:\n%s", want, logs)
		}
	}
}

func TestUnknownRouteUsesErrorSchema(t *testing.T) {
	e := newEnv(t)
	assertError(t, e.do(t, call{method: "GET", path: "/nope"}), http.StatusNotFound, "NOT_FOUND")
}
