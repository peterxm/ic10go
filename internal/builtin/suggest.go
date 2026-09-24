package builtin

import (
	"sort"
	"strings"
)

// maxSuggestDistance bounds how far a misspelling may be from a candidate
// before the compiler stops offering a correction (edit distance, characters).
const maxSuggestDistance = 4

// Levenshtein returns the edit distance between a and b.
func Levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			m := prev[j] + 1
			if cur[j-1]+1 < m {
				m = cur[j-1] + 1
			}
			if prev[j-1]+cost < m {
				m = prev[j-1] + cost
			}
			cur[j] = m
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// Closest returns the candidate nearest to name by edit distance, or "" when
// name is already a candidate or none is within maxSuggestDistance. Candidates
// are sorted first, so ties resolve to the lexicographically smallest and the
// result is deterministic.
func Closest(name string, candidates []string) string {
	sorted := append([]string(nil), candidates...)
	sort.Strings(sorted)

	best := ""
	bestDist := maxSuggestDistance + 1
	for _, cand := range sorted {
		if cand == name {
			return ""
		}
		if d := Levenshtein(name, cand); d < bestDist {
			bestDist = d
			best = cand
		}
	}
	if bestDist > maxSuggestDistance {
		return ""
	}
	return best
}

// ClosestIn returns the member of set nearest to name, or "".
func ClosestIn(name string, set map[string]bool) string {
	candidates := make([]string, 0, len(set))
	for k := range set {
		candidates = append(candidates, k)
	}
	return Closest(name, candidates)
}

// ClosestLogicType suggests a LogicType member for a misspelled device
// property, or "".
func ClosestLogicType(name string) string { return ClosestIn(name, LogicTypes) }

// ClosestSlotType suggests a LogicSlotType member for a misspelled slot
// property, or "".
func ClosestSlotType(name string) string { return ClosestIn(name, SlotTypes) }

// ClosestEnumMember suggests the nearest member of the same enum group for a
// misspelled "Receiver.Member", or "" when the receiver has no close member.
func ClosestEnumMember(full string) string {
	dot := strings.LastIndexByte(full, '.')
	if dot < 0 {
		return ""
	}
	receiver, member := full[:dot], full[dot+1:]

	var candidates []string
	for k := range EnumConstants {
		if strings.HasPrefix(k, receiver+".") {
			candidates = append(candidates, k[dot+1:])
		}
	}
	best := Closest(member, candidates)
	if best == "" {
		return ""
	}
	return receiver + "." + best
}
