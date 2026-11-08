package handlers

import (
	"github.com/gofiber/fiber/v2"

	"github.com/alfirus/vectorizer/internal/security"
)

// jwtClaims returns the JWT claims stashed by the auth middleware, or nil
// when the server runs in API-key mode (no JWT to enforce).
func jwtClaims(c *fiber.Ctx) *security.Claims {
	if claims, ok := c.Locals("jwt").(*security.Claims); ok {
		return claims
	}
	return nil
}

// requireWorkspace enforces the JWT workspace claim against a
// handler-resolved workspace (JSON body, query string, or :id ownership
// lookup). Returns false + writes 403 when the caller's token is scoped
// to a different workspace.
func requireWorkspace(c *fiber.Ctx, wsID string) bool {
	if !jwtClaims(c).Allows(wsID) {
		return forbiddenWorkspace(c)
	}
	return true
}

func forbiddenWorkspace(c *fiber.Ctx) bool {
	_ = c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "workspace mismatch"})
	return false
}
