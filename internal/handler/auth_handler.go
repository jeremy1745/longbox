package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/service"
)

type AuthHandler struct {
	authSvc *service.AuthService
}

func NewAuthHandler(authSvc *service.AuthService) *AuthHandler {
	return &AuthHandler{authSvc: authSvc}
}

// GET /api/v1/auth/status
func (h *AuthHandler) Status(w http.ResponseWriter, r *http.Request) {
	enabled, err := h.authSvc.AuthEnabled()
	if err != nil {
		writeInternalError(w, "AUTH_ERROR", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"auth_enabled": enabled,
	})
}

// POST /api/v1/auth/register
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	// Only allow registration if no users exist (first user) or caller is admin
	enabled, err := h.authSvc.AuthEnabled()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "AUTH_ERROR", err.Error())
		return
	}
	if enabled {
		user := UserFromContext(r.Context())
		if user == nil || !user.IsAdmin {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "only admins can create users when auth is enabled")
			return
		}
	}

	newUser, err := h.authSvc.Register(req.Username, req.Password)
	if err != nil {
		// Register errors are validation messages (username/password requirements), safe to return
		writeError(w, http.StatusBadRequest, "REGISTER_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"user": newUser,
	})
}

// POST /api/v1/auth/login
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	user, session, err := h.authSvc.Login(req.Username, req.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "LOGIN_FAILED", err.Error())
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "longbox_session",
		Value:    session.Token,
		Path:     "/",
		Expires:  session.ExpiresAt,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user": user,
	})
}

// POST /api/v1/auth/logout
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("longbox_session")
	if err == nil && cookie.Value != "" {
		_ = h.authSvc.Logout(cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "longbox_session",
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})

	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

// GET /api/v1/auth/me
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "not authenticated")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user": user,
	})
}

// GET /api/v1/auth/users (admin only)
func (h *AuthHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.authSvc.ListUsers()
	if err != nil {
		writeInternalError(w, "LIST_FAILED", err)
		return
	}
	if users == nil {
		users = []model.User{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"users": users,
	})
}

// POST /api/v1/auth/users (admin only — delegates to Register)
func (h *AuthHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	h.Register(w, r)
}

// DELETE /api/v1/auth/users/{id} (admin only)
func (h *AuthHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid user ID")
		return
	}

	// Prevent deleting yourself
	currentUser := UserFromContext(r.Context())
	if currentUser != nil && currentUser.ID == id {
		writeError(w, http.StatusBadRequest, "SELF_DELETE", "cannot delete your own account")
		return
	}

	if err := h.authSvc.DeleteUser(id); err != nil {
		writeInternalError(w, "DELETE_FAILED", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// PUT /api/v1/auth/users/{id}/password (self or admin)
func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid user ID")
		return
	}

	currentUser := UserFromContext(r.Context())
	if currentUser == nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "not authenticated")
		return
	}
	if currentUser.ID != id && !currentUser.IsAdmin {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "can only change your own password")
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	if err := h.authSvc.ChangePassword(id, req.Password); err != nil {
		writeError(w, http.StatusBadRequest, "CHANGE_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "password changed"})
}
