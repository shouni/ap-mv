package handlers

import (
	"context"

	"github.com/shouni/gcp-kit/auth/session"
)

// csrfTokenFromContext gets the CSRF token stored in the context.
//
// It is stored by gcp-kit/auth/session's Authenticate, so getting it has to look at the same key.
// It delegates here only to keep, in the handlers package, the name the template rendering side
// uses.
func csrfTokenFromContext(ctx context.Context) string {
	return session.CSRFTokenFromContext(ctx)
}
