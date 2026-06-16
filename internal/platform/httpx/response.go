package httpx

import (
	"encoding/json"
	"net/http"
)

// WriteJSON writes data as a JSON response body with the given status code.
func WriteJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}

}

// Pagination describes a page of results within a larger collection.
type Pagination struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

// NewPagination computes a Pagination from the current page, page size,
// and total item count.
func NewPagination(page, pageSize, total int) Pagination {
	totalPages := 0
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	return Pagination{Page: page, PageSize: pageSize, Total: total, TotalPages: totalPages}
}

// ListResponse is the standard envelope for paginated list endpoints.
type ListResponse struct {
	Data       any        `json:"data"`
	Pagination Pagination `json:"pagination"`
}

// WriteList writes data and pagination info as a ListResponse JSON body.
func WriteList(w http.ResponseWriter, status int, data any, p Pagination) {
	WriteJSON(w, status, ListResponse{Data: data, Pagination: p})
}
