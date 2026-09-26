package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
)

// decodeStrict decodes one JSON object into v. It rejects unknown fields, trailing
// data, wrong types, and bodies over the size limit. Use it in every handler that
// reads a request body.
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
