package main

import (
	"math"
	"time"
	"unicode/utf8"
)

const exactDistinctLimit = 100_000

// Column accumulates statistics for one column in a single pass.
type Column struct {
	Name string

	rows, nulls int64
	nBool       int64
	nInt        int64
	nFloat      int64 // includes ints
	nDate       int64
	nDT         int64 // includes dates
	firstBad    map[Kind]string

	mean, m2   float64 // Welford
	min, max   float64
	tMin, tMax time.Time
	lenMin     int
	lenMax     int

	distinct *distinct
}

func newColumn(name string) *Column {
	return &Column{Name: name, min: math.Inf(1), max: math.Inf(-1), lenMin: math.MaxInt, distinct: newDistinct(exactDistinctLimit), firstBad: map[Kind]string{}}
}

func (c *Column) add(v string, decimalComma bool, nulls nullSet) {
	c.rows++
	if nulls.is(v) {
		c.nulls++
		return
	}
	c.distinct.add(v)
	if n := utf8.RuneCountInString(v); n < c.lenMin {
		c.lenMin = n
	}
	if n := utf8.RuneCountInString(v); n > c.lenMax {
		c.lenMax = n
	}
	p := parseValue(v, decimalComma)
	if p.isBool {
		c.nBool++
	}
	if p.isInt {
		c.nInt++
	} else if _, ok := c.firstBad[KindInt]; !ok {
		c.firstBad[KindInt] = v
	}
	if p.isFloat {
		c.nFloat++
		k := float64(c.nFloat)
		d := p.num - c.mean
		c.mean += d / k
		c.m2 += d * (p.num - c.mean)
		c.min = math.Min(c.min, p.num)
		c.max = math.Max(c.max, p.num)
	} else if _, ok := c.firstBad[KindFloat]; !ok {
		c.firstBad[KindFloat] = v
	}
	if p.isDate || p.isDT {
		if p.isDate {
			c.nDate++
		}
		c.nDT++
		if c.tMin.IsZero() || p.t.Before(c.tMin) {
			c.tMin = p.t
		}
		if c.tMax.IsZero() || p.t.After(c.tMax) {
			c.tMax = p.t
		}
	} else if _, ok := c.firstBad[KindDate]; !ok {
		c.firstBad[KindDate] = v
	}
}

// Kind is the most specific type every non-null value fits.
func (c *Column) Kind() Kind {
	n := c.rows - c.nulls
	switch {
	case n == 0:
		return KindEmpty
	case c.nBool == n:
		return KindBool
	case c.nInt == n:
		return KindInt
	case c.nFloat == n:
		return KindFloat
	case c.nDate == n:
		return KindDate
	case c.nDT == n:
		return KindDateTime
	}
	return KindString
}

// Almost reports a column that is a number or date except for a few values (at least 95% fit),
// which usually means dirty data. It returns the type, the number of misfits and one example.
func (c *Column) Almost() (Kind, int64, string) {
	n := c.rows - c.nulls
	if c.Kind() != KindString || n < 20 {
		return 0, 0, ""
	}
	for _, k := range []struct {
		kind Kind
		hit  int64
	}{{KindInt, c.nInt}, {KindFloat, c.nFloat}, {KindDateTime, c.nDT}} {
		if float64(k.hit) >= 0.95*float64(n) {
			bad := c.firstBad[k.kind]
			if k.kind == KindDateTime {
				bad = c.firstBad[KindDate]
			}
			return k.kind, n - k.hit, bad
		}
	}
	return 0, 0, ""
}

func (c *Column) Stddev() float64 {
	if c.nFloat < 2 {
		return 0
	}
	return math.Sqrt(c.m2 / float64(c.nFloat-1))
}
