package web

import (
	"sort"
)

type NavElement struct {
	Weight int

	Text     string
	Children []NavChild
}

type NavChild struct {
	Text       string
	Target     string
	Permission Permission
}

func (s *Server) AddNavElement(n ...NavElement) {
	s.nav = append(s.nav, n...)
	sort.Slice(s.nav, func(i, j int) bool {
		return s.nav[i].Weight > s.nav[j].Weight
	})
}
