package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"log/slog"

	"github.com/midia/aione/internal/services/auth"
	"github.com/midia/aione/internal/services/users"
)

// AuthAPI groups authentication endpoints.
type AuthAPI struct {
	log          *slog.Logger
	service      *auth.Service
	loginHandler http.Handler
}

// NewAuthAPI wires auth handlers plus optional rate limiter middleware.
func NewAuthAPI(log *slog.Logger, service *auth.Service, limiter *auth.RateLimiter) *AuthAPI {
	h := &AuthAPI{log: log, service: service}
	h.loginHandler = http.HandlerFunc(h.login)
	if limiter != nil {
		h.loginHandler = limiter.Middleware(func(r *http.Request) string {
			return loginRateLimitKey(r)
		})(h.loginHandler)
	}
	return h
}

// RegisterHandler handles POST /auth/register.
func (a *AuthAPI) RegisterHandler() http.Handler {
	return http.HandlerFunc(a.register)
}

// LoginHandler handles POST /auth/login with rate limiting when configured.
func (a *AuthAPI) LoginHandler() http.Handler {
	return a.loginHandler
}

// RefreshHandler handles POST /auth/refresh.
func (a *AuthAPI) RefreshHandler() http.Handler {
	return http.HandlerFunc(a.refresh)
}

// LogoutHandler handles POST /auth/logout.
func (a *AuthAPI) LogoutHandler() http.Handler {
	return http.HandlerFunc(a.logout)
}

// register handles POST /auth/register requests.
// @Summary        Register a new user
// @Tags           auth
// @Accept         json
// @Produce        json
// @Param          request body auth.RegisterInput true "Registration payload"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        409 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /auth/register [post]
func (a *AuthAPI) register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.methodNotAllowed(w, http.MethodPost)
		return
	}
	var input auth.RegisterInput
	if err := decodeBody(r, &input); err != nil {
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
		return
	}
	input.IP = clientIP(r)
	input.UserAgent = r.UserAgent()
	resp, err := a.service.Register(r.Context(), input)
	if err != nil {
		a.handleError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, responseEnvelope{Data: resp})
}

// login handles POST /auth/login requests.
// @Summary        Log in
// @Tags           auth
// @Accept         json
// @Produce        json
// @Param          request body auth.LoginInput true "Login payload"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        401 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /auth/login [post]
func (a *AuthAPI) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.methodNotAllowed(w, http.MethodPost)
		return
	}
	var input auth.LoginInput
	if err := decodeBody(r, &input); err != nil {
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
		return
	}
	input.IP = clientIP(r)
	input.UserAgent = r.UserAgent()
	resp, err := a.service.Login(r.Context(), input)
	if err != nil {
		a.handleError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, responseEnvelope{Data: resp})
}

// refresh handles POST /auth/refresh token rotation.
// @Summary        Refresh tokens
// @Tags           auth
// @Accept         json
// @Produce        json
// @Param          request body auth.RefreshInput true "Refresh payload"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        401 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /auth/refresh [post]
func (a *AuthAPI) refresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.methodNotAllowed(w, http.MethodPost)
		return
	}
	var input auth.RefreshInput
	if err := decodeBody(r, &input); err != nil {
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
		return
	}
	input.IP = clientIP(r)
	input.UserAgent = r.UserAgent()
	resp, err := a.service.Refresh(r.Context(), input)
	if err != nil {
		a.handleError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, responseEnvelope{Data: resp})
}

// logout handles POST /auth/logout requests.
// @Summary        Logout
// @Tags           auth
// @Accept         json
// @Produce        json
// @Param          request body auth.RefreshInput true "Logout payload"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        401 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /auth/logout [post]
func (a *AuthAPI) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.methodNotAllowed(w, http.MethodPost)
		return
	}
	var payload auth.RefreshInput
	if err := decodeBody(r, &payload); err != nil {
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
		return
	}
	if payload.RefreshToken == "" {
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: "refresh_token required"})
		return
	}
	if err := a.service.Logout(r.Context(), payload.RefreshToken); err != nil {
		a.handleError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, responseEnvelope{Data: map[string]string{"status": "ok"}})
}

func (a *AuthAPI) writeJSON(w http.ResponseWriter, status int, payload responseEnvelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		a.log.Error("failed to encode response", slog.Any("error", err))
	}
}

func (a *AuthAPI) methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	a.writeJSON(w, http.StatusMethodNotAllowed, responseEnvelope{Error: "method not allowed"})
}

func (a *AuthAPI) handleError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, users.ErrUserExists):
		status = http.StatusConflict
	case errors.Is(err, auth.ErrInvalidCredentials):
		status = http.StatusUnauthorized
	case errors.Is(err, auth.ErrSessionMismatch), errors.Is(err, auth.ErrSessionNotFound):
		status = http.StatusUnauthorized
	default:
		status = http.StatusInternalServerError
	}
	if status >= http.StatusInternalServerError {
		a.log.Error("auth request failed", slog.Any("error", err))
	}
	a.writeJSON(w, status, responseEnvelope{Error: err.Error()})
}

func loginRateLimitKey(r *http.Request) string {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return "login:unknown"
	}
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	var payload auth.LoginInput
	_ = json.Unmarshal(bodyBytes, &payload)
	email := strings.ToLower(strings.TrimSpace(payload.Email))
	if email == "" {
		email = "unknown"
	}
	ip := clientIP(r)
	return email + "|" + ip
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}
