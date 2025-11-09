package web

import (
	"strconv"
)

func (s *Server) StrToUint(st string) uint {
	n, err := strconv.Atoi(st)
	if err != nil {
		return 0
	}
	return uint(n)
}
