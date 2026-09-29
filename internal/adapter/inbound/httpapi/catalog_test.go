package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/memory"
	catalogservice "github.com/Thapanut/go-engineering-starter/internal/core/catalog/service"
	"github.com/Thapanut/go-engineering-starter/internal/platform/auth"
)

func TestOrderFlowAC01_ProductsEndpoint(t *testing.T) {
	e := &env{jwt: auth.NewJWT(secret, issuer), logs: &syncBuffer{}}
	e.app = NewApp(Deps{Auth: e.jwt, Log: slog.New(slog.NewJSONHandler(e.logs, nil)), RequestTimeout: 5 * time.Second},
		CatalogModule{UseCase: catalogservice.NewCatalogService(memory.SampleProducts())})

	assertError(t, e.do(t, call{method: "GET", path: "/v1/products"}), http.StatusUnauthorized, "UNAUTHORIZED")
	r := e.do(t, call{method: "GET", path: "/v1/products", token: e.token(t)})
	var out productListResponse
	if r.status != http.StatusOK || json.Unmarshal(r.body, &out) != nil || len(out.Products) != 3 {
		t.Fatalf("status = %d body = %s", r.status, r.body)
	}
	if out.Products[1] != (productResponse{ProductID: "CERAMIC-MUG", Name: "Ceramic Mug 350 ml", Price: "290.00", PriceMinor: 29000, Currency: "THB"}) {
		t.Fatalf("product = %+v", out.Products[1])
	}
}
