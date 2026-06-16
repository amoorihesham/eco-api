package httpx

import (
	"encoding/json"
	"net/http"
)

func WriteJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}

}

type Pagination struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

func NewPagination(page, pageSize, total int) Pagination {
	totalPages := 0
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	return Pagination{Page: page, PageSize: pageSize, Total: total, TotalPages: totalPages}
}

type ListResponse struct {
	Data       any        `json:"data"`
	Pagination Pagination `json:"pagination"`
}

func WriteList(w http.ResponseWriter, status int, data any, p Pagination) {
	WriteJSON(w, status, ListResponse{Data: data, Pagination: p})
}
