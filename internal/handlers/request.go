// Package handlers provides shared HTTP request helpers.
package handlers

import (
	"encoding/json"
	"net/http"
)

const (
	authBodyLimit     int64 = 64 << 10
	standardBodyLimit int64 = 1 << 20
	importBodyLimit   int64 = 32 << 20
)

func decodeJSON(w http.ResponseWriter, r *http.Request, destination interface{}, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	return json.NewDecoder(r.Body).Decode(destination)
}
