package score

// Grade returns a letter grade for the given score value.
func Grade(score float64) string {
	switch {
	case score >= 0.93:
		return "A+"
	case score >= 0.87:
		return "A"
	case score >= 0.83:
		return "A-"
	case score >= 0.77:
		return "B+"
	case score >= 0.73:
		return "B"
	case score >= 0.67:
		return "B-"
	case score >= 0.63:
		return "C+"
	case score >= 0.57:
		return "C"
	case score >= 0.53:
		return "C-"
	case score >= 0.47:
		return "D+"
	case score >= 0.43:
		return "D"
	case score >= 0.37:
		return "D-"
	default:
		return "F"
	}
}
