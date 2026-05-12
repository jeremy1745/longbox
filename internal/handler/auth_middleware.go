package handler

import (
	"context"
	"net/http"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/service"
)

type contextKey string

const userContextKey contextKey = "user"

func AuthMiddleware(authSvc *service.AuthService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			enabled, err := authSvc.AuthEnabled()
			if err != nil {
				writeError(w, http.StatusInternalServerError, "AUTH_ERROR", "failed to check auth status")
				return
			}

			// Auth disabled — pass through
			if !enabled {
				next.ServeHTTP(w, r)
				return
			}

			cookie, err := r.Cookie("longbox_session")
			if err != nil || cookie.Value == "" {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
				return
			}

			user, err := authSvc.ValidateSession(cookie.Value)
			if err != nil || user == nil {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired session")
				return
			}

			ctx := context.WithValue(r.Context(), userContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AdminOnly gates an admin-only route. It assumes the chain already passed
// through AuthMiddleware; if auth is disabled globally, AuthMiddleware
// passes through without setting a user in context, and AdminOnly used to
// 403 every admin request even though auth is supposed to be off. This
// variant is wired in main.go with the auth service and short-circuits
// when auth is disabled.
//
// Plain AdminOnly is kept for tests/legacy wiring.
func AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil || !user.IsAdmin {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "admin access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AdminOnlyWithAuth is the auth-aware variant of AdminOnly. When the global
// auth_enabled setting is off (single-user local deployment), every admin
// endpoint is open — the user/admin distinction only matters when multiple
// accounts can sign in. The plain AdminOnly above does not consult the
// setting and therefore rejects admin calls (HTTP 403) even when auth is
// disabled, which made /api/v1/admin/shutdown un-callable without a
// session in single-user mode.
func AdminOnlyWithAuth(authSvc *service.AuthService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			enabled, err := authSvc.AuthEnabled()
			if err == nil && !enabled {
				next.ServeHTTP(w, r)
				return
			}
			user := UserFromContext(r.Context())
			if user == nil || !user.IsAdmin {
				writeError(w, http.StatusForbidden, "FORBIDDEN", "admin access required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func UserFromContext(ctx context.Context) *model.User {
	user, _ := ctx.Value(userContextKey).(*model.User)
	return user
}
