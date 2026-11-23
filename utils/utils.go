package utils

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func NormalizeCode(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }

func ValidName(name string) bool {
	name = strings.TrimSpace(name)
	return len([]rune(name)) > 0 && len([]rune(name)) <= 32 && !strings.ContainsAny(name, "\r\n\x00")
}
