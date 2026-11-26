package desktop

// fitText shortens s with an ellipsis until it fits the available width. The width function
// should be monotonic for progressively longer prefixes (as text advance normally is).
// Binary search keeps work bounded for long user-provided text.
func fitText(s string, max int, width func(string) int) string {
	if width(s) <= max {
		return s
	}

	runes := []rune(s)
	if width("…") > max {
		return "…"
	}

	// Find the longest prefix that fits alongside the ellipsis. A failed full-string measurement
	// means the upper bound is already known to be too wide.
	low, high := 0, len(runes)-1
	for low < high {
		mid := low + (high-low+1)/2
		if width(string(runes[:mid])+"…") <= max {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return string(runes[:low]) + "…"
}
