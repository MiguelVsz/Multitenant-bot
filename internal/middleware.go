package internal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
)

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func validateMetaSignature(body []byte, signatureHeader, appSecret string) bool {
	if signatureHeader == "" || appSecret == "" {
		return false
	}

	const prefix = "sha256="
	if !strings.HasPrefix(signatureHeader, prefix) {
		return false
	}

	expected := strings.TrimPrefix(signatureHeader, prefix)

	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	actual := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(actual), []byte(expected))
}
