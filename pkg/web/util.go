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

func (s *Server) ParseUintSlice(st []string) []uint {
	out := make([]uint, len(st))
	for i, v := range st {
		out[i] = s.StrToUint(v)
	}
	return out
}
