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
