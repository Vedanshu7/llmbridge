package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Issue creates a signed HS256 JWT with the given claims.
// ttl sets how long the token is valid; pass 0 for no expiry.
func Issue(claims map[string]interface{}, secret []byte, ttl time.Duration) (string, error) {
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	headerJSON, _ := json.Marshal(header)

	payload := make(map[string]interface{}, len(claims)+2)
	for k, v := range claims {
		payload[k] = v
	}
	now := time.Now().Unix()
	payload["iat"] = now
	if ttl > 0 {
		payload["exp"] = now + int64(ttl.Seconds())
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("jwt: marshal payload: %w", err)
	}

	h := b64URL(headerJSON) + "." + b64URL(payloadJSON)
	sig := jwtSign([]byte(h), secret)
	return h + "." + b64URL(sig), nil
}

// Validate parses and verifies an HS256 JWT, returning its claims.
// Returns an error if the signature is invalid or the token is expired.
func Validate(token string, secret []byte) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("jwt: malformed token")
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("jwt: decode signature: %w", err)
	}
	expected := jwtSign([]byte(parts[0]+"."+parts[1]), secret)
	if !hmac.Equal(sig, expected) {
		return nil, fmt.Errorf("jwt: invalid signature")
	}

	payloadRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("jwt: decode payload: %w", err)
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		return nil, fmt.Errorf("jwt: parse claims: %w", err)
	}

	if exp, ok := claims["exp"]; ok {
		var expUnix int64
		switch v := exp.(type) {
		case float64:
			expUnix = int64(v)
		case json.Number:
			expUnix, _ = v.Int64()
		}
		if expUnix > 0 && time.Now().Unix() > expUnix {
			return nil, fmt.Errorf("jwt: token expired")
		}
	}
	return claims, nil
}

func jwtSign(data, secret []byte) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write(data)
	return h.Sum(nil)
}

func b64URL(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}
