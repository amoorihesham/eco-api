package httpx

import (
	"net/http"
	"strconv"
)

const (
	defaultPage     = 1
	defaultPageSize = 20
	maxPageSize     = 100
)

func PageParams(r *http.Request) (page, pageSize int) {
	page = atoiOr(r.URL.Query().Get("page"), defaultPage)
	if page < 1 {
		page = defaultPage
	}
	pageSize = atoiOr(r.URL.Query().Get("page_size"), defaultPageSize)
	switch {
	case pageSize < 1:
		pageSize = defaultPageSize
	case pageSize > maxPageSize:
		pageSize = maxPageSize
	}
	return page, pageSize
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}
