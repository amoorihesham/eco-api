package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
	"github.com/amoorihesham/eco-api/internal/modules/identity/service"
	"github.com/amoorihesham/eco-api/internal/platform/auth"
	"github.com/amoorihesham/eco-api/internal/platform/httpx"
)

// --- DTOs (mirror the OpenAPI Account schemas) ---

type updateMeRequest struct {
	Name string `json:"name"`
}

type addressInput struct {
	Recipient  string `json:"recipient"`
	Line1      string `json:"line1"`
	Line2      string `json:"line2"`
	City       string `json:"city"`
	Region     string `json:"region"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
	Phone      string `json:"phone"`
	IsDefault  bool   `json:"is_default"`
}

type addressDTO struct {
	ID         string `json:"id"`
	Recipient  string `json:"recipient"`
	Line1      string `json:"line1"`
	Line2      string `json:"line2,omitempty"`
	City       string `json:"city"`
	Region     string `json:"region,omitempty"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
	Phone      string `json:"phone,omitempty"`
	IsDefault  bool   `json:"is_default"`
}

type addressListResponse struct {
	Data []addressDTO `json:"data"`
}

// --- profile ---

func (h *Handler) getMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	u, err := h.svc.GetProfile(r.Context(), userID)
	if err != nil {
		writeAccountError(w, err, "could not load profile")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toUserDTO(u))
}

func (h *Handler) updateMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	var req updateMeRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed",
			httpx.ErrorDetail{Field: "name", Message: "name is required"})
		return
	}
	u, err := h.svc.UpdateProfile(r.Context(), userID, strings.TrimSpace(req.Name))
	if err != nil {
		writeAccountError(w, err, "could not update profile")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toUserDTO(u))
}

// --- address book ---

func (h *Handler) listAddresses(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	addrs, err := h.svc.ListAddresses(r.Context(), userID)
	if err != nil {
		httpx.Internal(w, "could not list addresses")
		return
	}
	out := make([]addressDTO, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, toAddressDTO(a))
	}
	httpx.WriteJSON(w, http.StatusOK, addressListResponse{Data: out})
}

func (h *Handler) createAddress(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	var req addressInput
	if !decode(w, r, &req) {
		return
	}
	if errs := validateAddress(req); len(errs) > 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed", errs...)
		return
	}
	a, err := h.svc.CreateAddress(r.Context(), userID, toAddressInput(req))
	if err != nil {
		httpx.Internal(w, "could not create address")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toAddressDTO(a))
}

func (h *Handler) getAddress(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	addressID, ok := pathAddressID(w, r)
	if !ok {
		return
	}
	a, err := h.svc.GetAddress(r.Context(), userID, addressID)
	if err != nil {
		writeAccountError(w, err, "could not load address")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAddressDTO(a))
}

func (h *Handler) updateAddress(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	addressID, ok := pathAddressID(w, r)
	if !ok {
		return
	}
	var req addressInput
	if !decode(w, r, &req) {
		return
	}
	if errs := validateAddress(req); len(errs) > 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed", errs...)
		return
	}
	a, err := h.svc.UpdateAddress(r.Context(), userID, addressID, toAddressInput(req))
	if err != nil {
		writeAccountError(w, err, "could not update address")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAddressDTO(a))
}

func (h *Handler) deleteAddress(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	addressID, ok := pathAddressID(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeleteAddress(r.Context(), userID, addressID); err != nil {
		writeAccountError(w, err, "could not delete address")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

// callerID reads the authenticated user id placed by Authn; writes 401 when absent.
func callerID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := auth.UserID(r.Context())
	if !ok {
		httpx.Unauthorized(w, "authentication required")
		return uuid.Nil, false
	}
	return id, true
}

// pathAddressID parses {addressId}; a malformed id is treated as not found (no existence leak).
func pathAddressID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("addressId"))
	if err != nil {
		httpx.NotFound(w, "address not found")
		return uuid.Nil, false
	}
	return id, true
}

// writeAccountError maps domain sentinels to the standard envelope; not-found becomes 404.
func writeAccountError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, domain.ErrAddressNotFound), errors.Is(err, domain.ErrUserNotFound):
		httpx.NotFound(w, "not found")
	default:
		httpx.Internal(w, fallback)
	}
}

func validateAddress(req addressInput) []httpx.ErrorDetail {
	var errs []httpx.ErrorDetail
	require := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			errs = append(errs, httpx.ErrorDetail{Field: field, Message: field + " is required"})
		}
	}
	require("recipient", req.Recipient)
	require("line1", req.Line1)
	require("city", req.City)
	require("postal_code", req.PostalCode)
	if len(strings.TrimSpace(req.Country)) != 2 {
		errs = append(errs, httpx.ErrorDetail{Field: "country", Message: "country must be a 2-letter ISO code"})
	}
	return errs
}

func toAddressInput(req addressInput) service.AddressInput {
	return service.AddressInput{
		Recipient:  strings.TrimSpace(req.Recipient),
		Line1:      strings.TrimSpace(req.Line1),
		Line2:      strings.TrimSpace(req.Line2),
		City:       strings.TrimSpace(req.City),
		Region:     strings.TrimSpace(req.Region),
		PostalCode: strings.TrimSpace(req.PostalCode),
		Country:    strings.ToUpper(strings.TrimSpace(req.Country)),
		Phone:      strings.TrimSpace(req.Phone),
		IsDefault:  req.IsDefault,
	}
}

func toAddressDTO(a domain.Address) addressDTO {
	return addressDTO{
		ID:         a.ID.String(),
		Recipient:  a.Recipient,
		Line1:      a.Line1,
		Line2:      a.Line2,
		City:       a.City,
		Region:     a.Region,
		PostalCode: a.PostalCode,
		Country:    a.Country,
		Phone:      a.Phone,
		IsDefault:  a.IsDefault,
	}
}

func toUserDTO(u domain.User) userDTO {
	return userDTO{
		ID:        u.ID.String(),
		Email:     u.Email,
		Name:      u.Name,
		Role:      string(u.Role),
		CreatedAt: u.CreatedAt.Format(time.RFC3339),
	}
}
