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

// AdminOnly factory variant. When auth is enabled, requires a valid admin
// user. When auth is disabled (single-user LAN deployment), passes through
// — there's no user concept to gate on, and the practical alternative is
// "admin endpoints unreachable except via UI cookie hand-rolled by hand,"
// which broke automated deploys (POST /api/v1/admin/shutdown was returning
// 403 because the anonymous request had no user in context).
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

// AdminOnly preserved as the legacy zero-arg form for callers that don't
// have the auth service handy. Behaves the same as AdminOnlyWithAuth when
// a user is present in context. Treat new callers as if they should use
// AdminOnlyWithAuth.
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

func UserFromContext(ctx context.Context) *model.User {
	user, _ := ctx.Value(userContextKey).(*model.User)
	return user
}
