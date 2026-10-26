package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Kind is an inferred column type, ordered from most to least specific.
type Kind int

const (
	KindEmpty Kind = iota
	KindBool
	KindInt
	KindFloat
	KindDate
	KindDateTime
	KindString
)

func (k Kind) String() string {
	return [...]string{"empty", "bool", "int", "float", "date", "datetime", "string"}[k]
}

func (k Kind) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

func (k *Kind) UnmarshalText(b []byte) error {
	for i := KindEmpty; i <= KindString; i++ {
		if i.String() == string(b) {
			*k = i
			return nil
		}
	}
	return fmt.Errorf("unknown type %q", b)
}

// defaultNulls are the spellings of "missing" treated as empty. "NA" is deliberately not one of them:
// it is the ISO code for Namibia and the continent code for North America. Add it with -null NA.
var defaultNulls = []string{"", "null", "none", "nan", "n/a", "#n/a", "nil"}

type nullSet struct {
	set        map[string]bool
	extraFirst map[byte]bool // lowercased first bytes of user-supplied tokens
}

func newNullSet(extra []string) nullSet {
	s := nullSet{set: map[string]bool{}, extraFirst: map[byte]bool{}}
	for _, v := range defaultNulls {
		s.set[v] = true
	}
	for _, v := range extra {
		v = strings.ToLower(strings.TrimSpace(v))
		s.set[v] = true
		if v != "" {
			s.extraFirst[v[0]|0x20] = true
		}
	}
	return s
}

func (n nullSet) is(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > 16 {
		return false
	}
	// fast path: values that cannot be a null token skip the lowercase allocation
	c := s[0] | 0x20
	if c != 'n' && c != '#' && c != ' ' && !n.extraFirst[c] {
		return false
	}
	return n.set[strings.ToLower(strings.TrimSpace(s))]
}

var dateLayouts = []string{"2006-01-02", "2006/01/02", "02.01.2006", "02/01/2006", "01/02/2006", "2006.01.02", "20060102"}
var dateTimeLayouts = []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02 15:04", "02.01.2006 15:04:05", "02.01.2006 15:04", "2006-01-02 15:04:05.999999999"}

var boolWords = []string{"true", "false", "yes", "no", "t", "f", "y", "n"}

// parsed is one value seen through every type we try.
type parsed struct {
	isBool  bool
	isInt   bool
	isFloat bool
	num     float64
	isDate  bool
	isDT    bool
	t       time.Time
}

func parseValue(s string, decimalComma bool) parsed {
	var p parsed
	v := strings.TrimSpace(s)
	if len(v) <= 5 {
		for _, b := range boolWords {
			if strings.EqualFold(v, b) {
				p.isBool = true
				return p
			}
		}
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil && !leadingZero(v) {
		p.isInt, p.isFloat, p.num = true, true, float64(n)
		return p
	}
	fv := v
	if decimalComma && strings.Count(v, ",") == 1 && !strings.Contains(v, ".") {
		fv = strings.Replace(v, ",", ".", 1)
	}
	if looksNumeric(fv) && !zeroPadded(fv) {
		if f, err := strconv.ParseFloat(fv, 64); err == nil {
			p.isFloat, p.num = true, f
			return p
		}
	}
	if len(v) >= 8 && len(v) <= 35 && v[0] >= '0' && v[0] <= '9' {
		if len(v) <= 10 {
			for _, l := range dateLayouts {
				if t, err := time.Parse(l, v); err == nil {
					p.isDate, p.t = true, t
					return p
				}
			}
		}
		for _, l := range dateTimeLayouts {
			if t, err := time.Parse(l, v); err == nil {
				p.isDT, p.t = true, t
				return p
			}
		}
	}
	return p
}

// leadingZero keeps codes like "007" or zip codes as strings: they are identifiers, not numbers.
func leadingZero(v string) bool { return zeroPadded(v) }

// zeroPadded is true for "007" or "-01.5" style values (a zero followed by another digit).
func zeroPadded(v string) bool {
	v = strings.TrimPrefix(strings.TrimPrefix(v, "-"), "+")
	return len(v) > 1 && v[0] == '0' && v[1] >= '0' && v[1] <= '9'
}

// looksNumeric rejects things strconv accepts but humans do not mean as numbers (Inf, NaN, hex, underscores).
func looksNumeric(v string) bool {
	if v == "" {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if !(c >= '0' && c <= '9' || c == '.' || c == '-' || c == '+' || c == 'e' || c == 'E') {
			return false
		}
	}
	return true
}
