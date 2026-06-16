// Package handler exposes the identity module's use cases over HTTP.
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
	"github.com/amoorihesham/eco-api/internal/modules/identity/service"
	"github.com/amoorihesham/eco-api/internal/platform/httpx"
)

// Handler exposes the identity module's HTTP endpoints.
type Handler struct{ svc *service.Service }

// New builds a Handler backed by svc.
func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

// --- request/response DTOs (mirror the OpenAPI Authentication schemas) ---

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}
type forgotRequest struct {
	Email string `json:"email"`
}
type resetRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

type userDTO struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
}
type tokensDTO struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}
type authResponse struct {
	User   userDTO   `json:"user"`
	Tokens tokensDTO `json:"tokens"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decode(w, r, &req) {
		return
	}
	if errs := validateRegister(req); len(errs) > 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed", errs...)
		return
	}
	res, err := h.svc.Register(r.Context(), strings.TrimSpace(req.Email), req.Password, strings.TrimSpace(req.Name))
	if err != nil {
		if errors.Is(err, domain.ErrEmailTaken) {
			httpx.WriteError(w, http.StatusConflict, httpx.CodeConflict, "email already registered")
			return
		}
		httpx.Internal(w, "could not register")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toAuthResponse(res))
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Email) == "" || req.Password == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "email and password are required")
		return
	}
	res, err := h.svc.Login(r.Context(), strings.TrimSpace(req.Email), req.Password)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidCredentials) {
			httpx.Unauthorized(w, "invalid email or password")
			return
		}
		httpx.Internal(w, "could not log in")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAuthResponse(res))
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.RefreshToken) == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "refresh_token is required")
		return
	}
	res, err := h.svc.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidToken) {
			httpx.Unauthorized(w, "invalid or expired refresh token")
			return
		}
		httpx.Internal(w, "could not refresh")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, tokensDTO{
		AccessToken: res.AccessToken, RefreshToken: res.RefreshToken, TokenType: "bearer", ExpiresIn: res.ExpiresIn,
	})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !decode(w, r, &req) {
		return
	}
	if err := h.svc.Logout(r.Context(), req.RefreshToken); err != nil {
		httpx.Internal(w, "could not log out")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Email) == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "email is required")
		return
	}
	if _, err := h.svc.ForgotPassword(r.Context(), strings.TrimSpace(req.Email)); err != nil {
		httpx.Internal(w, "could not process request")
		return
	}
	// P3: the reset token is issued + persisted; P16 emails it. NEVER log it in production.
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) resetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetRequest
	if !decode(w, r, &req) {
		return
	}
	if errs := validateReset(req); len(errs) > 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed", errs...)
		return
	}
	if err := h.svc.ResetPassword(r.Context(), req.Token, req.NewPassword); err != nil {
		if errors.Is(err, domain.ErrInvalidToken) {
			httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "invalid or expired reset token")
			return
		}
		httpx.Internal(w, "could not reset password")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "invalid JSON body")
		return false
	}
	return true
}

func validateRegister(req registerRequest) []httpx.ErrorDetail {
	var errs []httpx.ErrorDetail
	if strings.TrimSpace(req.Email) == "" {
		errs = append(errs, httpx.ErrorDetail{Field: "email", Message: "email is required"})
	}
	if len(req.Password) < 8 {
		errs = append(errs, httpx.ErrorDetail{Field: "password", Message: "password must be at least 8 characters"})
	}
	if strings.TrimSpace(req.Name) == "" {
		errs = append(errs, httpx.ErrorDetail{Field: "name", Message: "name is required"})
	}
	return errs
}

func validateReset(req resetRequest) []httpx.ErrorDetail {
	var errs []httpx.ErrorDetail
	if strings.TrimSpace(req.Token) == "" {
		errs = append(errs, httpx.ErrorDetail{Field: "token", Message: "token is required"})
	}
	if len(req.NewPassword) < 8 {
		errs = append(errs, httpx.ErrorDetail{Field: "new_password", Message: "password must be at least 8 characters"})
	}
	return errs
}

func toAuthResponse(res service.AuthResult) authResponse {
	return authResponse{
		User: userDTO{
			ID:        res.User.ID.String(),
			Email:     res.User.Email,
			Name:      res.User.Name,
			Role:      string(res.User.Role),
			CreatedAt: res.User.CreatedAt.Format(time.RFC3339),
		},
		Tokens: tokensDTO{
			AccessToken: res.AccessToken, RefreshToken: res.RefreshToken, TokenType: "bearer", ExpiresIn: res.ExpiresIn,
		},
	}
}
