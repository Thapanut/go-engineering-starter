package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
)

type handlers struct {
	transfers port.TransferUseCase
	accounts  port.AccountQuery
	log       *slog.Logger
}

// DTOs mirror contracts/openapi.yaml. Domain types never leak into JSON directly.
type moneyDTO struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

type accountDTO struct {
	ID      string   `json:"id"`
	Balance moneyDTO `json:"balance"`
	Status  string   `json:"status"`
}

type createTransferRequest struct {
	FromAccountID string   `json:"fromAccountId"`
	ToAccountID   string   `json:"toAccountId"`
	Amount        moneyDTO `json:"amount"`
	Reference     string   `json:"reference"`
}

type transferDTO struct {
	ID            string   `json:"id"`
	FromAccountID string   `json:"fromAccountId"`
	ToAccountID   string   `json:"toAccountId"`
	Amount        moneyDTO `json:"amount"`
	Reference     string   `json:"reference,omitempty"`
	Status        string   `json:"status"`
	CreatedAt     string   `json:"createdAt"`
}

func toMoneyDTO(m domain.Money) moneyDTO {
	return moneyDTO{Amount: m.Amount, Currency: string(m.Currency)}
}

func toTransferDTO(t domain.Transfer) transferDTO {
	return transferDTO{
		ID: t.ID, FromAccountID: t.FromAccountID, ToAccountID: t.ToAccountID,
		Amount: toMoneyDTO(t.Amount), Reference: t.Reference, Status: string(t.Status),
		CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func (h *handlers) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *handlers) getAccount(c *gin.Context) {
	a, err := h.accounts.GetAccount(c.Request.Context(), customerIDOf(c), c.Param("accountId"))
	if err != nil {
		writeError(c, h.log, err)
		return
	}
	c.JSON(http.StatusOK, accountDTO{ID: a.ID, Balance: toMoneyDTO(a.Balance), Status: string(a.Status)})
}

func (h *handlers) getTransfer(c *gin.Context) {
	t, err := h.transfers.GetTransfer(c.Request.Context(), customerIDOf(c), c.Param("transferId"))
	if err != nil {
		writeError(c, h.log, err)
		return
	}
	c.JSON(http.StatusOK, toTransferDTO(t))
}

func (h *handlers) createTransfer(c *gin.Context) {
	var body createTransferRequest
	if err := decodeStrict(c.Request.Body, &body); err != nil {
		writeError(c, h.log, err)
		return
	}
	res, err := h.transfers.CreateTransfer(c.Request.Context(), domain.TransferRequest{
		CustomerID:     customerIDOf(c),
		IdempotencyKey: c.GetHeader("Idempotency-Key"),
		FromAccountID:  body.FromAccountID,
		ToAccountID:    body.ToAccountID,
		Amount:         domain.Money{Amount: body.Amount.Amount, Currency: domain.Currency(body.Amount.Currency)},
		Reference:      body.Reference,
	}, traceIDOf(c))
	if err != nil {
		writeError(c, h.log, err)
		return
	}
	status := http.StatusCreated
	if res.Replayed {
		status = http.StatusOK
		c.Header("Idempotent-Replayed", "true")
	}
	c.JSON(status, toTransferDTO(res.Transfer))
}

// decodeStrict rejects unknown fields, trailing data, wrong types, and oversized bodies.
func decodeStrict(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return domain.Invalid("request body too large")
		}
		return domain.Invalid("malformed JSON body")
	}
	if dec.More() {
		return domain.Invalid("request body must contain a single JSON object")
	}
	return nil
}
