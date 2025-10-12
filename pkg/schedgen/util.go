package schedgen

// Why go doesn't have an integer absolute value is a mystery...
func abs(i int) int {
	if i < 0 {
		return -i
	}
	return i
}
