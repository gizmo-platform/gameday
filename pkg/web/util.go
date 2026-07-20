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

func Paginate(r *http.Request) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		q := r.URL.Query()
		page, _ := strconv.Atoi(q.Get("page"))
		if page <= 0 {
			page = 1
		}

		pageSize, _ := strconv.Atoi(q.Get("page_size"))
		switch {
		case pageSize > 100:
			pageSize = 100
		case pageSize <= 0:
			pageSize = 10
		}

		offset := (page - 1) * pageSize
		return db.Offset(offset).Limit(pageSize)
	}
}
