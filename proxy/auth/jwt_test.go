package auth

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestIssueAndValidate(t *testing.T) {
	secret := []byte("test-secret")
	claims := map[string]interface{}{"sub": "user1", "role": "admin"}

	token, err := Issue(claims, secret, time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if strings.Count(token, ".") != 2 {
		t.Fatalf("expected 3-part JWT, got %q", token)
	}

	got, err := Validate(token, secret)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got["sub"] != "user1" {
		t.Fatalf("expected sub=user1, got %v", got["sub"])
	}
}

func TestIssueAddsIat(t *testing.T) {
	token, _ := Issue(map[string]interface{}{"x": 1}, []byte("s"), 0)
	claims, _ := Validate(token, []byte("s"))
	if _, ok := claims["iat"]; !ok {
		t.Fatal("expected iat claim to be set")
	}
}

func TestIssueWithTTLAddsExp(t *testing.T) {
	token, _ := Issue(map[string]interface{}{}, []byte("s"), time.Minute)
	claims, _ := Validate(token, []byte("s"))
	if _, ok := claims["exp"]; !ok {
		t.Fatal("expected exp claim when TTL is set")
	}
}

func TestIssueWithoutTTLNoExp(t *testing.T) {
	token, _ := Issue(map[string]interface{}{}, []byte("s"), 0)
	claims, _ := Validate(token, []byte("s"))
	if _, ok := claims["exp"]; ok {
		t.Fatal("expected no exp claim when TTL is 0")
	}
}

func TestValidateWrongSecret(t *testing.T) {
	token, _ := Issue(map[string]interface{}{"x": 1}, []byte("secret"), time.Hour)
	_, err := Validate(token, []byte("wrong"))
	if err == nil {
		t.Fatal("expected error for wrong secret")
	}
}

func TestValidateExpiredToken(t *testing.T) {
	// Use internal helpers (same-package test) to craft a token with exp in the past.
	secret := []byte("s")
	hdr := map[string]string{"alg": "HS256", "typ": "JWT"}
	headerJSON, _ := json.Marshal(hdr)
	payload := map[string]interface{}{"x": 1, "iat": time.Now().Unix(), "exp": time.Now().Unix() - 10}
	payloadJSON, _ := json.Marshal(payload)
	h := b64URL(headerJSON) + "." + b64URL(payloadJSON)
	sig := jwtSign([]byte(h), secret)
	token := h + "." + b64URL(sig)

	_, err := Validate(token, secret)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected 'expired' in error, got: %v", err)
	}
}

func TestValidateMalformedToken(t *testing.T) {
	_, err := Validate("not.a.valid.jwt.token.here", []byte("s"))
	if err == nil {
		t.Fatal("expected error for malformed token")
	}
}

func TestValidateTwoPartToken(t *testing.T) {
	_, err := Validate("header.payload", []byte("s"))
	if err == nil {
		t.Fatal("expected error for two-part token")
	}
}
