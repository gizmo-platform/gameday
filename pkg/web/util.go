package web

import (
	"net/http"
	"strconv"

	"gorm.io/gorm"
)

func (s *Server) StrToUint(st string) uint {
	n, err := strconv.Atoi(st)
	if err != nil {
		return 0
	}
	return uint(n)
}

func (s *Server) ParseUintSlice(st []string) []uint {
	out := make([]uint, len(st))
	for i, v := range st {
		out[i] = s.StrToUint(v)
	}
	return out
}

// PageParams returns the effective page number and page size for the
// request, clamped the same way Paginate applies them: page starts at
// 1, page size defaults to 10 and is capped at 100.
func PageParams(r *http.Request) (page, pageSize int) {
	q := r.URL.Query()
	page, _ = strconv.Atoi(q.Get("page"))
	if page <= 0 {
		page = 1
	}

	pageSize, _ = strconv.Atoi(q.Get("page_size"))
	switch {
	case pageSize > 100:
		pageSize = 100
	case pageSize <= 0:
		pageSize = 10
	}
	return page, pageSize
}

func Paginate(r *http.Request) func(db *gorm.DB) *gorm.DB {
	page, pageSize := PageParams(r)
	offset := (page - 1) * pageSize
	return func(db *gorm.DB) *gorm.DB {
		return db.Offset(offset).Limit(pageSize)
	}
}
