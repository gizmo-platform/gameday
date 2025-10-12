package cmd

import (
	"strconv"
)

func strToInt(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}
