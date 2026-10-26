package main

import (
	"math"
	"math/bits"
)

// hll is a HyperLogLog cardinality estimator with 2^14 registers (~0.8% standard error, 16 KiB).
type hll struct {
	reg [1 << hllP]uint8
}

const hllP = 14

// hash64 is FNV-1a followed by the splitmix64 finalizer, so short similar strings spread across all bits.
func hash64(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	h ^= h >> 30
	h *= 0xbf58476d1ce4e5b9
	h ^= h >> 27
	h *= 0x94d049bb133111eb
	h ^= h >> 31
	return h
}

func (h *hll) add(x uint64) {
	idx := x >> (64 - hllP)
	rank := uint8(bits.LeadingZeros64(x<<hllP|1<<(hllP-1)) + 1)
	if rank > h.reg[idx] {
		h.reg[idx] = rank
	}
}

func (h *hll) estimate() float64 {
	const m = float64(1 << hllP)
	sum, zeros := 0.0, 0
	for _, r := range h.reg {
		sum += math.Ldexp(1, -int(r))
		if r == 0 {
			zeros++
		}
	}
	alpha := 0.7213 / (1 + 1.079/m)
	e := alpha * m * m / sum
	if e <= 2.5*m && zeros > 0 { // small-range correction: linear counting
		e = m * math.Log(m/float64(zeros))
	}
	return e
}

// distinct counts every value exactly up to a limit of distinct values, then switches to
// HyperLogLog for the distinct count and Space-Saving (seeded with the exact counts so far) for top values.
type distinct struct {
	exact map[string]int64
	h     *hll
	top   *topK
	limit int
}

func newDistinct(limit int) *distinct { return &distinct{exact: map[string]int64{}, limit: limit} }

func (d *distinct) add(s string) {
	if d.h != nil {
		d.h.add(hash64(s))
		d.top.add(s)
		return
	}
	d.exact[s]++
	if len(d.exact) > d.limit {
		d.h = &hll{}
		for k := range d.exact {
			d.h.add(hash64(k))
		}
		d.top = seedTopK(256, d.exact)
		d.exact = nil
	}
}

// topValues returns the n most frequent values; the counts are exact unless approx is true,
// in which case each count is a guaranteed lower bound.
func (d *distinct) topValues(n int) (out []ssEntry, approx bool) {
	if d.h != nil {
		return d.top.top(n), true
	}
	for v, c := range d.exact {
		if c > 1 {
			out = append(out, ssEntry{value: v, count: c})
		}
	}
	sortEntries(out)
	if len(out) > n {
		out = out[:n]
	}
	return out, false
}

// count returns the number of distinct values and whether it is an estimate.
func (d *distinct) count() (int64, bool) {
	if d.h == nil {
		return int64(len(d.exact)), false
	}
	return int64(math.Round(d.h.estimate())), true
}
