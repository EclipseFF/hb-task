package auth

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
)

type identityKey struct{}

type Identity struct {
	Subject string
	Roles   []string
}

func FromContext(ctx context.Context) Identity {
	id, _ := ctx.Value(identityKey{}).(Identity)
	return id
}

type Verifier interface {
	Verify(context.Context, string) (*oidc.IDToken, error)
}

func New(ctx context.Context, issuer, jwksURL string) *oidc.IDTokenVerifier {
	return oidc.NewVerifier(issuer, oidc.NewRemoteKeySet(ctx, jwksURL), &oidc.Config{
		ClientID: "vacation-api", SupportedSigningAlgs: []string{"RS256"},
	})
}

func Require(v Verifier, role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		r := c.Request
		fail := func(code int, msg string) {
			if code == http.StatusUnauthorized {
				c.Header("WWW-Authenticate", "Bearer")
			}
			c.AbortWithStatusJSON(code, gin.H{"error": msg})
		}
		scheme, raw, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || raw == "" {
			fail(http.StatusUnauthorized, "bearer token required")
			return
		}
		token, err := v.Verify(r.Context(), raw)
		if err != nil || token.Subject == "" {
			fail(http.StatusUnauthorized, "invalid or expired token")
			return
		}
		var claims struct {
			RealmAccess struct {
				Roles []string `json:"roles"`
			} `json:"realm_access"`
		}
		if err := token.Claims(&claims); err != nil {
			fail(http.StatusUnauthorized, "invalid claims")
			return
		}
		if !slices.Contains(claims.RealmAccess.Roles, role) {
			fail(http.StatusForbidden, "role required: "+role)
			return
		}
		id := Identity{token.Subject, claims.RealmAccess.Roles}
		c.Request = r.WithContext(context.WithValue(r.Context(), identityKey{}, id))
		c.Next()
	}
}
