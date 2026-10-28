package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestJWTVerificationAndRoles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.RawURLEncoding.EncodeToString
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{
			"kty": "RSA", "kid": "test", "alg": "RS256", "use": "sig",
			"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	defer jwks.Close()
	verifier := New(t.Context(), "https://issuer.test", jwks.URL)
	for _, tc := range []struct {
		name, issuer, audience, role string
		expiry                       time.Time
		tampered                     bool
		status                       int
	}{
		{"valid", "https://issuer.test", "vacation-api", "employee", time.Now().Add(time.Hour), false, 200},
		{"wrong issuer", "https://attacker.test", "vacation-api", "employee", time.Now().Add(time.Hour), false, 401},
		{"wrong audience", "https://issuer.test", "another-api", "employee", time.Now().Add(time.Hour), false, 401},
		{"expired", "https://issuer.test", "vacation-api", "employee", time.Now().Add(-time.Hour), false, 401},
		{"wrong role", "https://issuer.test", "vacation-api", "manager", time.Now().Add(time.Hour), false, 403},
		{"tampered", "https://issuer.test", "vacation-api", "employee", time.Now().Add(time.Hour), true, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "test", "typ": "JWT"})
			claims, _ := json.Marshal(map[string]any{"sub": "employee-id", "iss": tc.issuer, "aud": tc.audience,
				"exp": tc.expiry.Unix(), "iat": time.Now().Unix(), "realm_access": map[string]any{"roles": []string{tc.role}}})
			unsigned := b64(header) + "." + b64(claims)
			digest := sha256.Sum256([]byte(unsigned))
			signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
			if err != nil {
				t.Fatal(err)
			}
			if tc.tampered {
				signature[0] ^= 1
			}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", "Bearer "+unsigned+"."+b64(signature))
			response := httptest.NewRecorder()
			router := gin.New()
			router.GET("/", Require(verifier, "employee"), func(c *gin.Context) {
				if FromContext(c.Request.Context()).Subject != "employee-id" {
					t.Error("missing verified identity")
				}
				c.Status(http.StatusOK)
			})
			router.ServeHTTP(response, req)
			if response.Code != tc.status {
				t.Fatalf("got %d: %s, want %d", response.Code, response.Body.String(), tc.status)
			}
		})
	}
}
