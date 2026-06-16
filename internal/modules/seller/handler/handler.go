// Package handler implements the seller module's net/http transport: decode → service → encode.
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/amoorihesham/eco-api/internal/modules/seller/domain"
	"github.com/amoorihesham/eco-api/internal/modules/seller/service"
	"github.com/amoorihesham/eco-api/internal/platform/auth"
	"github.com/amoorihesham/eco-api/internal/platform/httpx"
)

// Handler implements the seller module's HTTP transport.
type Handler struct{ svc *service.Service }

// New builds a Handler over svc.
func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

// --- DTOs (mirror the OpenAPI Seller schemas) ---

type applicationInput struct {
	StoreName   string `json:"store_name"`
	Description string `json:"description"`
	Contact     string `json:"contact"`
}

type rejectInput struct {
	Reason string `json:"reason"`
}

type storeInput struct {
	Name        string `json:"name"`
	LogoURL     string `json:"logo_url"`
	Description string `json:"description"`
	Contact     string `json:"contact"`
}

type applicationDTO struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	Status      string `json:"status"`
	StoreName   string `json:"store_name"`
	Description string `json:"description,omitempty"`
	Contact     string `json:"contact"`
	CreatedAt   string `json:"created_at"`
}

type storeDTO struct {
	ID          string `json:"id"`
	SellerID    string `json:"seller_id"`
	Name        string `json:"name"`
	LogoURL     string `json:"logo_url,omitempty"`
	Description string `json:"description,omitempty"`
	Contact     string `json:"contact"`
}

// --- seller self-service ---

func (h *Handler) apply(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	var req applicationInput
	if !decode(w, r, &req) {
		return
	}
	var errs []httpx.ErrorDetail
	if strings.TrimSpace(req.StoreName) == "" {
		errs = append(errs, httpx.ErrorDetail{Field: "store_name", Message: "store_name is required"})
	}
	if strings.TrimSpace(req.Contact) == "" {
		errs = append(errs, httpx.ErrorDetail{Field: "contact", Message: "contact is required"})
	}
	if len(errs) > 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed", errs...)
		return
	}
	a, err := h.svc.Apply(r.Context(), userID, service.ApplicationInput{
		StoreName:   strings.TrimSpace(req.StoreName),
		Description: strings.TrimSpace(req.Description),
		Contact:     strings.TrimSpace(req.Contact),
	})
	if err != nil {
		writeSellerError(w, err, "could not submit application")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toApplicationDTO(a))
}

func (h *Handler) getMyApplication(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	a, err := h.svc.GetMyApplication(r.Context(), userID)
	if err != nil {
		writeSellerError(w, err, "could not load application")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toApplicationDTO(a))
}

func (h *Handler) getMyStore(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	st, err := h.svc.GetStore(r.Context(), userID)
	if err != nil {
		writeSellerError(w, err, "could not load store")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toStoreDTO(st))
}

func (h *Handler) updateMyStore(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	var req storeInput
	if !decode(w, r, &req) {
		return
	}
	var errs []httpx.ErrorDetail
	if strings.TrimSpace(req.Name) == "" {
		errs = append(errs, httpx.ErrorDetail{Field: "name", Message: "name is required"})
	}
	if strings.TrimSpace(req.Contact) == "" {
		errs = append(errs, httpx.ErrorDetail{Field: "contact", Message: "contact is required"})
	}
	if len(errs) > 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed", errs...)
		return
	}
	st, err := h.svc.UpdateStore(r.Context(), userID, service.StoreInput{
		Name:        strings.TrimSpace(req.Name),
		LogoURL:     strings.TrimSpace(req.LogoURL),
		Description: strings.TrimSpace(req.Description),
		Contact:     strings.TrimSpace(req.Contact),
	})
	if err != nil {
		writeSellerError(w, err, "could not update store")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toStoreDTO(st))
}

// --- admin lifecycle ---

func (h *Handler) approve(w http.ResponseWriter, r *http.Request) {
	id, ok := pathSellerID(w, r)
	if !ok {
		return
	}
	a, err := h.svc.Approve(r.Context(), id)
	if err != nil {
		writeSellerError(w, err, "could not approve seller")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toApplicationDTO(a))
}

func (h *Handler) reject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathSellerID(w, r)
	if !ok {
		return
	}
	var req rejectInput
	// Body is optional; ignore a decode error on an empty body.
	_ = json.NewDecoder(r.Body).Decode(&req)
	a, err := h.svc.Reject(r.Context(), id, strings.TrimSpace(req.Reason))
	if err != nil {
		writeSellerError(w, err, "could not reject seller")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toApplicationDTO(a))
}

func (h *Handler) suspend(w http.ResponseWriter, r *http.Request) {
	id, ok := pathSellerID(w, r)
	if !ok {
		return
	}
	a, err := h.svc.Suspend(r.Context(), id)
	if err != nil {
		writeSellerError(w, err, "could not suspend seller")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toApplicationDTO(a))
}

// --- helpers ---

func callerID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := auth.UserID(r.Context())
	if !ok {
		httpx.Unauthorized(w, "authentication required")
		return uuid.Nil, false
	}
	return id, true
}

func pathSellerID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("sellerId"))
	if err != nil {
		httpx.NotFound(w, "seller application not found")
		return uuid.Nil, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "invalid JSON body")
		return false
	}
	return true
}

// writeSellerError maps domain sentinels to the standard envelope.
func writeSellerError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, domain.ErrApplicationNotFound), errors.Is(err, domain.ErrStoreNotFound):
		httpx.NotFound(w, "not found")
	case errors.Is(err, domain.ErrAlreadySeller), errors.Is(err, domain.ErrApplicationExists):
		httpx.WriteError(w, http.StatusConflict, httpx.CodeConflict, err.Error())
	case errors.Is(err, domain.ErrNotApprovable), errors.Is(err, domain.ErrNotRejectable), errors.Is(err, domain.ErrNotSuspendable):
		httpx.WriteError(w, http.StatusConflict, httpx.CodeConflict, err.Error())
	case errors.Is(err, domain.ErrNotApproved):
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, "seller is not approved")
	default:
		httpx.Internal(w, fallback)
	}
}

func toApplicationDTO(a domain.Application) applicationDTO {
	return applicationDTO{
		ID:          a.ID.String(),
		UserID:      a.UserID.String(),
		Status:      string(a.Status),
		StoreName:   a.StoreName,
		Description: a.Description,
		Contact:     a.Contact,
		CreatedAt:   a.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func toStoreDTO(s domain.Store) storeDTO {
	return storeDTO{
		ID:          s.ID.String(),
		SellerID:    s.SellerID.String(),
		Name:        s.Name,
		LogoURL:     s.LogoURL,
		Description: s.Description,
		Contact:     s.Contact,
	}
}
