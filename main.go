// Command csvglance profiles CSV files in one streaming pass: types, nulls, distinct counts, ranges and top values.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"
)

var version = "0.1.0"

type ColumnJSON struct {
	Name          string     `json:"name"`
	Type          Kind       `json:"type"`
	Filled        int64      `json:"filled"`
	Nulls         int64      `json:"nulls"`
	Distinct      int64      `json:"distinct"`
	DistinctExact bool       `json:"distinct_exact"`
	Min           *float64   `json:"min,omitempty"`
	Max           *float64   `json:"max,omitempty"`
	Mean          *float64   `json:"mean,omitempty"`
	Stddev        *float64   `json:"stddev,omitempty"`
	First         string     `json:"first,omitempty"`
	Last          string     `json:"last,omitempty"`
	MinLength     *int       `json:"min_length,omitempty"`
	MaxLength     *int       `json:"max_length,omitempty"`
	Top           []TopValue `json:"top,omitempty"`
	TopApprox     bool       `json:"top_approx,omitempty"` // counts are lower bounds
}

type TopValue struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("csvglance", flag.ContinueOnError)
	fs.SetOutput(stderr)
	delim := fs.String("d", "", "delimiter: , ; tab | (default: detect)")
	noHeader := fs.Bool("no-header", false, "first row is data, not column names")
	decimal := fs.String("decimal", "auto", "decimal separator: auto (comma when delimiter is ;), dot, comma")
	maxRows := fs.Int64("n", 0, "read at most n data rows (0 = all)")
	asJSON := fs.Bool("json", false, "JSON output")
	top := fs.Int("top", 3, "top values to show per column")
	nullFlag := fs.String("null", "", "extra comma-separated values to treat as missing, e.g. NA,-,?")
	showVersion := fs.Bool("version", false, "print version")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "csvglance %s - profile CSV files in one streaming pass\n\nUsage: csvglance [flags] file.csv [file2.csv.gz ...]   (use - for stdin)\n\nFlags:\n", version)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, version)
		return 0
	}
	files := fs.Args()
	if len(files) == 0 {
		fs.Usage()
		return 2
	}
	o := options{noHeader: *noHeader, maxRows: *maxRows, decimalComma: -1}
	if *nullFlag != "" {
		o.nulls = strings.Split(*nullFlag, ",")
	}
	switch *delim {
	case "":
	case "tab", `\t`:
		o.delim = '\t'
	default:
		r, n := utf8.DecodeRuneInString(*delim)
		if n != len(*delim) {
			fmt.Fprintln(stderr, "csvglance: -d must be a single character or 'tab'")
			return 2
		}
		o.delim = r
	}
	switch *decimal {
	case "auto":
	case "dot", ".":
		o.decimalComma = 0
	case "comma", ",":
		o.decimalComma = 1
	default:
		fmt.Fprintln(stderr, "csvglance: -decimal must be auto, dot or comma")
		return 2
	}

	var all []any
	status := 0
	for i, path := range files {
		start := time.Now()
		src, err := open(path)
		if err != nil {
			fmt.Fprintf(stderr, "csvglance: %v\n", err)
			status = 1
			continue
		}
		p, err := profileReader(src, o)
		src.close()
		if err != nil {
			fmt.Fprintf(stderr, "csvglance: %v\n", err)
			status = 1
			continue
		}
		elapsed := time.Since(start)
		if *asJSON {
			cols := make([]ColumnJSON, len(p.Columns))
			for j, c := range p.Columns {
				cols[j] = columnJSON(c, *top)
			}
			all = append(all, struct {
				*Profile
				Columns []ColumnJSON `json:"columns"`
			}{p, cols})
			continue
		}
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		printProfile(stdout, p, *top, elapsed)
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		var v any = all
		if len(all) == 1 {
			v = all[0]
		}
		enc.Encode(v)
	}
	return status
}

func columnJSON(c *Column, top int) ColumnJSON {
	n, est := c.distinct.count()
	j := ColumnJSON{Name: c.Name, Type: c.Kind(), Filled: c.rows - c.nulls, Nulls: c.nulls, Distinct: n, DistinctExact: !est}
	switch c.Kind() {
	case KindInt, KindFloat:
		mn, mx, mean, sd := c.min, c.max, c.mean, c.Stddev()
		j.Min, j.Max, j.Mean, j.Stddev = &mn, &mx, &mean, &sd
	case KindDate:
		j.First, j.Last = c.tMin.Format("2006-01-02"), c.tMax.Format("2006-01-02")
	case KindDateTime:
		j.First, j.Last = c.tMin.Format(time.RFC3339), c.tMax.Format(time.RFC3339)
	case KindString, KindBool:
		a, b := c.lenMin, c.lenMax
		j.MinLength, j.MaxLength = &a, &b
	}
	vals, approx := c.distinct.topValues(top)
	for _, e := range vals {
		j.Top = append(j.Top, TopValue{e.value, e.count})
	}
	j.TopApprox = approx
	return j
}

func printProfile(w io.Writer, p *Profile, top int, elapsed time.Duration) {
	size := ""
	if p.Bytes > 0 {
		size = " · " + human(float64(p.Bytes), "B")
	}
	dec := ""
	if p.DecimalComma {
		dec = " · decimal comma"
	}
	trunc := ""
	if p.Truncated {
		trunc = " (first rows only)"
	}
	fmt.Fprintf(w, "%s · %s rows%s · %d columns · %s-separated%s%s · %s\n\n",
		p.File, group(p.Rows), trunc, len(p.Columns), p.Delimiter, dec, size, elapsed.Round(time.Millisecond))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tCOLUMN\tTYPE\tFILLED\tDISTINCT\tRANGE\tMEAN\tTOP VALUES")
	var notes []string
	for i, c := range p.Columns {
		n, est := c.distinct.count()
		dist := group(n)
		if est {
			dist = "~" + human(float64(n), "")
		}
		filled := c.rows - c.nulls
		fill := "-"
		if c.rows > 0 {
			fill = pct(float64(filled) / float64(c.rows))
		}
		rng, mean := "", ""
		switch c.Kind() {
		case KindInt, KindFloat:
			rng = num(c.min) + " … " + num(c.max)
			mean = num(c.mean)
		case KindDate:
			rng = c.tMin.Format("2006-01-02") + " … " + c.tMax.Format("2006-01-02")
		case KindDateTime:
			rng = c.tMin.Format("2006-01-02 15:04") + " … " + c.tMax.Format("2006-01-02 15:04")
		case KindString, KindBool:
			if c.lenMin == c.lenMax {
				rng = fmt.Sprintf("len %d", c.lenMin)
			} else {
				rng = fmt.Sprintf("len %d … %d", c.lenMin, c.lenMax)
			}
		}
		var tops []string
		vals, approx := c.distinct.topValues(top)
		for _, e := range vals {
			ge := ""
			if approx {
				ge = "≥"
			}
			tops = append(tops, fmt.Sprintf("%s (%s%s)", clip(e.value, 18), ge, human(float64(e.count), "")))
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", i+1, clip(c.Name, 28), c.Kind(), fill, dist, rng, mean, strings.Join(tops, ", "))

		if k, bad, ex := c.Almost(); bad > 0 {
			notes = append(notes, fmt.Sprintf("%s looks like %s but %s value(s) are not, e.g. %q", c.Name, k, group(bad), clip(ex, 30)))
		}
		switch {
		case filled == 0 && c.rows > 0:
			notes = append(notes, c.Name+" is always empty")
		case n == 1 && !est && filled == c.rows && c.rows > 1:
			notes = append(notes, fmt.Sprintf("%s has a single value everywhere (%q)", c.Name, clip(firstKey(c), 30)))
		case !est && n == filled && filled == c.rows && c.rows > 100:
			notes = append(notes, c.Name+" is unique and never empty: a good key")
		}
	}
	tw.Flush()
	if p.RaggedRows > 0 {
		notes = append(notes, fmt.Sprintf("%s row(s) have a different number of fields than the header", group(p.RaggedRows)))
	}
	if len(notes) > 0 {
		fmt.Fprintln(w)
		for _, n := range notes {
			fmt.Fprintln(w, "• "+n)
		}
	}
}

func firstKey(c *Column) string {
	for k := range c.distinct.exact {
		return k
	}
	return ""
}

func clip(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\t", " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func pct(f float64) string {
	switch {
	case f == 1:
		return "100%"
	case f >= 0.999:
		return ">99.9%"
	case f > 0 && f < 0.001:
		return "<0.1%"
	}
	return strconv.FormatFloat(math.Floor(f*1000)/10, 'f', -1, 64) + "%"
}

// num prints numbers for humans: thousands grouping, and fewer decimals as the magnitude grows.
func num(f float64) string {
	a := math.Abs(f)
	switch {
	case a >= 1e15 || a > 0 && a < 0.001:
		return strconv.FormatFloat(f, 'g', 4, 64)
	case f == math.Trunc(f):
		return group(int64(f))
	}
	prec := 4
	switch {
	case a >= 100:
		prec = 1
	case a >= 1:
		prec = 2
	}
	s := strconv.FormatFloat(f, 'f', prec, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	intPart, frac, _ := strings.Cut(s, ".")
	n, _ := strconv.ParseInt(intPart, 10, 64)
	out := group(n)
	if n == 0 && strings.HasPrefix(intPart, "-") {
		out = "-0"
	}
	if frac != "" {
		out += "." + frac
	}
	return out
}

func group(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func human(f float64, unit string) string {
	if unit == "B" {
		for _, u := range []string{"B", "KB", "MB", "GB"} {
			if f < 1024 || u == "GB" {
				if u == "B" {
					return fmt.Sprintf("%.0f B", f)
				}
				return fmt.Sprintf("%.1f %s", f, u)
			}
			f /= 1024
		}
	}
	switch {
	case f >= 1e6:
		return strconv.FormatFloat(f/1e6, 'f', 1, 64) + "M"
	case f >= 1e4:
		return strconv.FormatFloat(f/1e3, 'f', 1, 64) + "k"
	}
	return group(int64(f))
}
