package httpapi_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/inbound/httpapi"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/memory"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/system"
	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/service"
	"github.com/Thapanut/go-engineering-starter/internal/platform/auth"
)

// Synthetic test data only.
const (
	issuer   = "test-issuer"
	alice    = "cust-alice"
	bob      = "cust-bob"
	accAlice = "11111111-1111-4111-8111-111111111111"
	accBob   = "22222222-2222-4222-8222-222222222222"
)

var secret = []byte("test-secret-at-least-32-bytes-long!!")

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
	h    http.Handler
	jwt  *auth.JWT
	logs *syncBuffer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st := memory.NewStore()
	st.SeedAccount(domain.Account{ID: accAlice, CustomerID: alice, Balance: domain.Money{Amount: 100_000, Currency: domain.THB}, Status: domain.AccountActive})
	st.SeedAccount(domain.Account{ID: accBob, CustomerID: bob, Balance: domain.Money{Amount: 5_000, Currency: domain.THB}, Status: domain.AccountActive})
	svc := service.NewTransferService(st, system.Clock{}, system.UUIDGenerator{})
	logs := &syncBuffer{}
	j := auth.NewJWT(secret, issuer)
	h := httpapi.NewRouter(httpapi.Deps{
		Transfers: svc, Accounts: svc, Auth: j,
		Log:            slog.New(slog.NewJSONHandler(logs, nil)),
		RequestTimeout: 5 * time.Second,
	})
	return &env{h: h, jwt: j, logs: logs}
}

func (e *env) token(t *testing.T, sub string) string {
	t.Helper()
	tok, err := e.jwt.Issue(sub, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

type call struct {
	method, path, token, key, body string
	headers                        map[string]string
}

func (e *env) do(t *testing.T, c call) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), c.method, c.path, strings.NewReader(c.body))
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.key != "" {
		req.Header.Set("Idempotency-Key", c.key)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func transferBody(amount int64) string {
	return fmt.Sprintf(`{"fromAccountId":%q,"toAccountId":%q,"amount":{"amount":%d,"currency":"THB"},"reference":"rent"}`, accAlice, accBob, amount)
}

type errResp struct {
	Code, Message, TraceID string
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func assertError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body.String())
	}
	e := decode[errResp](t, rec)
	if e.Code != code || e.Message == "" || e.TraceID == "" {
		t.Fatalf("error body = %+v, want code %s with message and traceId", e, code)
	}
	if rec.Header().Get("X-Trace-Id") != e.TraceID {
		t.Fatalf("X-Trace-Id header %q != body traceId %q", rec.Header().Get("X-Trace-Id"), e.TraceID)
	}
}

func TestHealthzIsPublic(t *testing.T) {
	e := newEnv(t)
	if rec := e.do(t, call{method: "GET", path: "/healthz"}); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestAC01_AC02_CreateThenReplay(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t, alice)
	first := e.do(t, call{method: "POST", path: "/v1/transfers", token: tok, key: "key-1", body: transferBody(25_000)})
	if first.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body %s", first.Code, first.Body.String())
	}
	created := decode[map[string]any](t, first)
	if created["status"] != "COMPLETED" || created["amount"].(map[string]any)["amount"] != float64(25_000) {
		t.Fatalf("unexpected body %v", created)
	}

	replay := e.do(t, call{method: "POST", path: "/v1/transfers", token: tok, key: "key-1", body: transferBody(25_000)})
	if replay.Code != http.StatusOK || replay.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay status = %d, header %q", replay.Code, replay.Header().Get("Idempotent-Replayed"))
	}
	if decode[map[string]any](t, replay)["id"] != created["id"] {
		t.Fatal("replay returned a different transfer")
	}

	acc := decode[map[string]any](t, e.do(t, call{method: "GET", path: "/v1/accounts/" + accAlice, token: tok}))
	if acc["balance"].(map[string]any)["amount"] != float64(75_000) {
		t.Fatalf("balance after replay = %v, want 75000", acc["balance"])
	}
}

func TestAC03_KeyReuseReturns422(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t, alice)
	e.do(t, call{method: "POST", path: "/v1/transfers", token: tok, key: "key-1", body: transferBody(1_000)})
	rec := e.do(t, call{method: "POST", path: "/v1/transfers", token: tok, key: "key-1", body: transferBody(2_000)})
	assertError(t, rec, http.StatusUnprocessableEntity, "IDEMPOTENCY_KEY_REUSED")
}

func TestAC04_InsufficientFundsReturns422(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, call{method: "POST", path: "/v1/transfers", token: e.token(t, alice), key: "k", body: transferBody(100_001)})
	assertError(t, rec, http.StatusUnprocessableEntity, "INSUFFICIENT_FUNDS")
}

func TestAC05_HTTPValidation(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t, alice)
	cases := map[string]call{
		"missing idempotency key": {body: transferBody(1)},
		"unknown field":           {key: "k", body: `{"fromAccountId":"` + accAlice + `","toAccountId":"` + accBob + `","amount":{"amount":1,"currency":"THB"},"isAdmin":true}`},
		"float amount":            {key: "k", body: `{"fromAccountId":"` + accAlice + `","toAccountId":"` + accBob + `","amount":{"amount":1.5,"currency":"THB"}}`},
		"malformed json":          {key: "k", body: `{"fromAccountId":`},
		"trailing data":           {key: "k", body: transferBody(1) + `{}`},
		"body too large":          {key: "k", body: `{"reference":"` + strings.Repeat("x", 17<<10) + `"}`},
		"missing amount":          {key: "k", body: `{"fromAccountId":"` + accAlice + `","toAccountId":"` + accBob + `"}`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			c.method, c.path, c.token = "POST", "/v1/transfers", tok
			assertError(t, e.do(t, c), http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
	t.Run("bad account id in path", func(t *testing.T) {
		assertError(t, e.do(t, call{method: "GET", path: "/v1/accounts/not-a-uuid", token: tok}), http.StatusBadRequest, "VALIDATION_ERROR")
	})
}

func TestAC06_Unauthorized(t *testing.T) {
	e := newEnv(t)
	expired, _ := e.jwt.Issue(alice, time.Minute, time.Now().Add(-time.Hour))
	wrongIssuer, _ := auth.NewJWT(secret, "someone-else").Issue(alice, time.Hour, time.Now())
	wrongSecret, _ := auth.NewJWT([]byte("another-secret-that-is-32-bytes-long"), issuer).Issue(alice, time.Hour, time.Now())
	for name, tok := range map[string]string{
		"no token": "", "garbage": "abc.def.ghi", "expired": expired,
		"wrong issuer": wrongIssuer, "wrong secret": wrongSecret,
		"alg none": unsignedToken(alice),
	} {
		t.Run(name, func(t *testing.T) {
			for _, c := range []call{
				{method: "GET", path: "/v1/accounts/" + accAlice},
				{method: "POST", path: "/v1/transfers", key: "k", body: transferBody(1)},
			} {
				c.token = tok
				assertError(t, e.do(t, c), http.StatusUnauthorized, "UNAUTHORIZED")
			}
		})
	}
}

// unsignedToken builds an "alg":"none" JWT at runtime (no token literal in source).
func unsignedToken(sub string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none","typ":"JWT"}`)) + "." +
		enc([]byte(fmt.Sprintf(`{"sub":%q,"iss":%q,"exp":%d}`, sub, issuer, time.Now().Add(time.Hour).Unix()))) + "."
}

func TestAC07_OthersAccountIs404(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, call{method: "GET", path: "/v1/accounts/" + accBob, token: e.token(t, alice)})
	assertError(t, rec, http.StatusNotFound, "ACCOUNT_NOT_FOUND")
}

func TestAC14_OthersTransferIs404(t *testing.T) {
	e := newEnv(t)
	created := decode[map[string]any](t, e.do(t, call{method: "POST", path: "/v1/transfers", token: e.token(t, alice), key: "k", body: transferBody(1_000)}))
	path := "/v1/transfers/" + created["id"].(string)
	if rec := e.do(t, call{method: "GET", path: path, token: e.token(t, alice)}); rec.Code != http.StatusOK {
		t.Fatalf("owner status = %d", rec.Code)
	}
	assertError(t, e.do(t, call{method: "GET", path: path, token: e.token(t, bob)}), http.StatusNotFound, "TRANSFER_NOT_FOUND")
}

func TestAC15_TraceIDPropagation(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, call{method: "GET", path: "/v1/accounts/" + accAlice, headers: map[string]string{"X-Trace-Id": "client-trace-12345"}})
	assertError(t, rec, http.StatusUnauthorized, "UNAUTHORIZED")
	if got := rec.Header().Get("X-Trace-Id"); got != "client-trace-12345" {
		t.Fatalf("trace id = %q, want client value", got)
	}
	// Malformed incoming ids are replaced, preventing log injection.
	rec = e.do(t, call{method: "GET", path: "/healthz", headers: map[string]string{"X-Trace-Id": "bad\nvalue"}})
	if got := rec.Header().Get("X-Trace-Id"); got == "" || strings.ContainsAny(got, "\n ") {
		t.Fatalf("trace id = %q, want generated", got)
	}
}

func TestAC16_LogsContainNoSensitiveData(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t, alice)
	e.do(t, call{method: "POST", path: "/v1/transfers", token: tok, key: "key-secret-1", body: transferBody(1_000)})
	e.do(t, call{method: "GET", path: "/v1/accounts/" + accAlice, token: tok})
	logs := e.logs.String()
	for _, forbidden := range []string{accAlice, accBob, tok, "key-secret-1", "rent", alice} {
		if strings.Contains(logs, forbidden) {
			t.Errorf("log contains sensitive value %q:\n%s", forbidden, logs)
		}
	}
	for _, want := range []string{`"route":"/v1/transfers"`, `"route":"/v1/accounts/:accountId"`, `"trace_id"`, `"latency_ms"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("log missing %s:\n%s", want, logs)
		}
	}
}

func TestUnknownRouteUsesErrorSchema(t *testing.T) {
	e := newEnv(t)
	assertError(t, e.do(t, call{method: "GET", path: "/nope"}), http.StatusNotFound, "NOT_FOUND")
}
