package security

import (
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	Workspace string `json:"w,omitempty"`
	Peer      string `json:"p,omitempty"`
	Admin     bool   `json:"ad,omitempty"`
	jwt.RegisteredClaims
}

// GenerateToken mints a JWT for local dev/testing. Compatible: use scripts/generate_jwt.py analogue.
func GenerateToken(secret, workspace, peer string, admin bool, expires time.Duration) (string, error) {
	claims := Claims{Workspace: workspace, Peer: peer, Admin: admin}
	if expires > 0 {
		claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(expires))
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString([]byte(secret))
}

func ParseToken(secret, tokenStr string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	return claims, nil
}

func ExtractBearer(authHeader string) string {
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return strings.TrimSpace(authHeader[7:])
	}
	return ""
}
