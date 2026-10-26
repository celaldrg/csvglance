package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectDelimiter(t *testing.T) {
	cases := map[string]rune{
		"a,b,c\n1,2,3\n4,5,6\n":                    ',',
		"ad;tutar;tarih\nAli;12,5;01.02.2024\n":    ';',
		"a\tb\n1\t2\n":                             '\t',
		"a|b|c\n1|2|3\n":                           '|',
		"name,note\n\"Smith; John\",\"a; b; c\"\n": ',',
	}
	for in, want := range cases {
		if got := detectDelimiter([]byte(in)); got != want {
			t.Errorf("detectDelimiter(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseValue(t *testing.T) {
	if p := parseValue("42", false); !p.isInt || p.num != 42 {
		t.Error("42 should be int")
	}
	if p := parseValue("007", false); p.isInt || p.isFloat {
		t.Error("zero-padded codes must stay strings")
	}
	if p := parseValue("0.5", false); !p.isFloat || p.isInt {
		t.Error("0.5 should be float")
	}
	if p := parseValue("12,5", true); !p.isFloat || p.num != 12.5 {
		t.Error("decimal comma not parsed")
	}
	if p := parseValue("12,5", false); p.isFloat {
		t.Error("12,5 must not be a number without decimal comma mode")
	}
	for _, s := range []string{"Inf", "NaN", "0x1F", "1_000"} {
		if p := parseValue(s, false); p.isFloat {
			t.Errorf("%q should not be numeric", s)
		}
	}
	if p := parseValue("2024-02-29", false); !p.isDate {
		t.Error("ISO date not detected")
	}
	if p := parseValue("29.02.2024", false); !p.isDate {
		t.Error("dotted date not detected")
	}
	if p := parseValue("2024-02-29T10:00:00Z", false); !p.isDT {
		t.Error("RFC3339 not detected")
	}
	if p := parseValue("yes", false); !p.isBool {
		t.Error("yes should be bool")
	}
}

func TestHyperLogLogAccuracy(t *testing.T) {
	for _, n := range []int{1_000, 50_000, 1_000_000} {
		h := &hll{}
		for i := 0; i < n; i++ {
			h.add(hash64(fmt.Sprintf("user-%d", i)))
		}
		got := h.estimate()
		if rel := math.Abs(got-float64(n)) / float64(n); rel > 0.025 {
			t.Errorf("n=%d: estimate %.0f off by %.2f%%", n, got, rel*100)
		}
	}
}

func TestDistinctSwitchesToSketch(t *testing.T) {
	d := newDistinct(1000)
	for i := 0; i < 20_000; i++ {
		d.add("hot") // one very frequent value
		d.add(fmt.Sprintf("v%d", i))
	}
	n, est := d.count()
	if !est || math.Abs(float64(n)-20_001)/20_001 > 0.03 {
		t.Errorf("count = %d (estimate=%v), want ~20001", n, est)
	}
	top, approx := d.topValues(1)
	if !approx || len(top) != 1 || top[0].value != "hot" || top[0].count > 20_000 || top[0].count < 19_000 {
		t.Errorf("top = %+v approx=%v, want hot with a lower bound close to 20000", top, approx)
	}
}

func profileString(t *testing.T, s string, o options) *Profile {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "x.csv")
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer src.close()
	p, err := profileReader(src, o)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func col(p *Profile, name string) *Column {
	for _, c := range p.Columns {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestProfileTurkishSemicolonFile(t *testing.T) {
	in := "\xef\xbb\xbfil;tutar;tarih;plaka;aktif\n" +
		"Ankara;12,50;01.02.2024;06;evet\n" +
		"İzmir;7,25;15.03.2024;35;true\n" +
		"Ankara;;31.12.2023;06;false\n" +
		"Bursa;100;NULL;16\n"
	p := profileString(t, in, options{decimalComma: -1})
	if p.Delimiter != "semicolon" || !p.DecimalComma || p.Rows != 4 || p.RaggedRows != 1 {
		t.Fatalf("profile = %+v", p)
	}
	if c := col(p, "il"); c == nil {
		t.Fatal("BOM was not stripped from the first header")
	}
	tutar := col(p, "tutar")
	if tutar.Kind() != KindFloat || tutar.nulls != 1 || tutar.min != 7.25 || tutar.max != 100 {
		t.Errorf("tutar: kind=%v nulls=%d min=%v max=%v", tutar.Kind(), tutar.nulls, tutar.min, tutar.max)
	}
	if k := col(p, "tarih").Kind(); k != KindDate {
		t.Errorf("tarih kind = %v", k)
	}
	if k := col(p, "plaka").Kind(); k != KindString {
		t.Errorf("plaka (zero-padded codes) kind = %v, want string", k)
	}
	if c := col(p, "aktif"); c.Kind() != KindString || c.nulls != 1 {
		t.Errorf("aktif kind=%v nulls=%d", c.Kind(), c.nulls)
	}
	top, _ := col(p, "il").distinct.topValues(3)
	if len(top) != 1 || top[0].value != "Ankara" || top[0].count != 2 {
		t.Errorf("top = %+v", top)
	}
}

func TestNAIsNotMissingByDefault(t *testing.T) {
	in := "continent\nNA\nEU\nNA\n"
	if c := col(profileString(t, in, options{decimalComma: -1}), "continent"); c.nulls != 0 {
		t.Errorf("NA counted as null by default")
	}
	if c := col(profileString(t, in, options{decimalComma: -1, nulls: []string{"NA"}}), "continent"); c.nulls != 2 {
		t.Errorf("-null NA not applied, nulls=%d", c.nulls)
	}
}

func TestAlmostNumericNote(t *testing.T) {
	var b strings.Builder
	b.WriteString("id,age\n")
	for i := 0; i < 99; i++ {
		fmt.Fprintf(&b, "%d,%d\n", i, 20+i%50)
	}
	b.WriteString("99,unknown\n")
	c := col(profileString(t, b.String(), options{decimalComma: -1}), "age")
	k, bad, ex := c.Almost()
	if c.Kind() != KindString || k != KindInt || bad != 1 || ex != "unknown" {
		t.Errorf("Almost() = %v %d %q", k, bad, ex)
	}
}

func TestGzipAndJSONOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.csv.gz")
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte("n,when\n1,2024-01-01 10:00:00\n2,2024-06-01 12:30:00\n3,2023-01-01 00:00:00\n"))
	zw.Close()
	os.WriteFile(path, buf.Bytes(), 0o644)

	var out, errb bytes.Buffer
	if code := run([]string{"-json", path}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var got struct {
		Rows    int64
		Columns []ColumnJSON
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err, out.String())
	}
	if got.Rows != 3 || got.Columns[0].Type != KindInt || *got.Columns[0].Mean != 2 || got.Columns[1].Type != KindDateTime || got.Columns[1].First != "2023-01-01T00:00:00Z" {
		t.Errorf("json = %s", out.String())
	}
}

func TestCLIErrors(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != 2 {
		t.Errorf("no args: exit %d", code)
	}
	if code := run([]string{"-d", "ab", "x.csv"}, &out, &errb); code != 2 {
		t.Errorf("bad -d: exit %d", code)
	}
	if code := run([]string{"does-not-exist.csv"}, &out, &errb); code != 1 {
		t.Errorf("missing file: exit %d", code)
	}
}

func TestTableOutput(t *testing.T) {
	var out, errb bytes.Buffer
	dir := t.TempDir()
	path := filepath.Join(dir, "people.csv")
	os.WriteFile(path, []byte("name,age,city\nAda,36,London\nLinus,28,Helsinki\nGrace,45,\n"), 0o644)
	if code := run([]string{path}, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	s := out.String()
	for _, want := range []string{"3 rows · 3 columns · comma-separated", "age     int", "28 … 45", "66.6%"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
}

func TestNum(t *testing.T) {
	for in, want := range map[float64]string{1291.4567: "1,291.5", 100250.5: "100,250.5", 36.333: "36.33", 0.12345: "0.1235", -1266: "-1,266", 2: "2", 1e-5: "1e-05", -0.5: "-0.5"} {
		if got := num(in); got != want {
			t.Errorf("num(%v) = %q, want %q", in, got, want)
		}
	}
}
