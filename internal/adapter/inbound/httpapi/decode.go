package httpapi

import (
	"bytes"
	"encoding/json"

	"github.com/gofiber/fiber/v2"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/domain"
)

// decodeStrict decodes the request body (one JSON object) into v. It rejects
// unknown fields, trailing data, and wrong types. Oversized bodies are already
// rejected by fiber.Config.BodyLimit. Use it instead of c.BodyParser, which
// silently ignores unknown fields.
func decodeStrict(c *fiber.Ctx, v any) error {
	dec := json.NewDecoder(bytes.NewReader(c.Body()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return domain.Invalid("malformed JSON body")
	}
	if dec.More() {
		return domain.Invalid("request body must contain a single JSON object")
	}
	return nil
}
