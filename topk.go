package main

import "sort"

// topK tracks frequent values with the Space-Saving algorithm (Metwally et al.): k counters,
// each with an over-estimation bound, so count-err is a guaranteed lower bound of the true count.
type topK struct {
	k      int
	counts map[string]*ssEntry
}

type ssEntry struct {
	value string
	count int64
	err   int64
}

// seedTopK starts a sketch from exact counts, keeping the k largest.
func seedTopK(k int, exact map[string]int64) *topK {
	all := make([]ssEntry, 0, len(exact))
	for v, c := range exact {
		all = append(all, ssEntry{value: v, count: c})
	}
	sortEntries(all)
	t := &topK{k: k, counts: make(map[string]*ssEntry, k)}
	for i := 0; i < len(all) && i < k; i++ {
		e := all[i]
		t.counts[e.value] = &e
	}
	return t
}

func (t *topK) add(v string) {
	if e, ok := t.counts[v]; ok {
		e.count++
		return
	}
	if len(t.counts) < t.k {
		t.counts[v] = &ssEntry{value: v, count: 1}
		return
	}
	var min *ssEntry
	for _, e := range t.counts {
		if min == nil || e.count < min.count {
			min = e
		}
	}
	delete(t.counts, min.value)
	t.counts[v] = &ssEntry{value: v, count: min.count + 1, err: min.count}
}

// top returns up to n entries ordered by guaranteed count, reporting that lower bound as the count.
// Entries guaranteed to appear only once are skipped.
func (t *topK) top(n int) []ssEntry {
	out := make([]ssEntry, 0, len(t.counts))
	for _, e := range t.counts {
		if lb := e.count - e.err; lb > 1 {
			out = append(out, ssEntry{value: e.value, count: lb})
		}
	}
	sortEntries(out)
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func sortEntries(s []ssEntry) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].count != s[j].count {
			return s[i].count > s[j].count
		}
		return s[i].value < s[j].value
	})
}
