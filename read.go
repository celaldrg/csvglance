package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
)

// detectDelimiter picks the candidate that splits the sample lines into the same number (>1) of fields
// most consistently. Quoted sections are skipped so commas inside "a, b" do not count.
func detectDelimiter(sample []byte) rune {
	lines := strings.Split(strings.ReplaceAll(string(sample), "\r\n", "\n"), "\n")
	if len(lines) > 1 {
		lines = lines[:len(lines)-1] // last line may be cut off
	}
	if len(lines) > 50 {
		lines = lines[:50]
	}
	best, bestScore := ',', -1.0
	for _, d := range []rune{',', ';', '\t', '|'} {
		counts := map[int]int{}
		for _, l := range lines {
			if strings.TrimSpace(l) == "" {
				continue
			}
			counts[countOutsideQuotes(l, d)]++
		}
		mode, modeN, total := 0, 0, 0
		for c, n := range counts {
			total += n
			if n > modeN || n == modeN && c > mode {
				mode, modeN = c, n
			}
		}
		if mode == 0 || total == 0 {
			continue
		}
		score := float64(modeN)/float64(total)*1000 + float64(mode)
		if score > bestScore {
			best, bestScore = d, score
		}
	}
	return best
}

func countOutsideQuotes(line string, d rune) int {
	n, inQ := 0, false
	for _, r := range line {
		switch {
		case r == '"':
			inQ = !inQ
		case r == d && !inQ:
			n++
		}
	}
	return n
}

type source struct {
	name  string
	r     io.Reader
	size  int64
	close func() error
}

func open(path string) (*source, error) {
	var f *os.File
	var size int64 = -1
	if path == "-" {
		f = os.Stdin
	} else {
		var err error
		if f, err = os.Open(path); err != nil {
			return nil, err
		}
		if st, err := f.Stat(); err == nil {
			size = st.Size()
		}
	}
	br := bufio.NewReaderSize(f, 1<<20)
	var r io.Reader = br
	if head, _ := br.Peek(2); len(head) == 2 && head[0] == 0x1f && head[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			f.Close()
			return nil, err
		}
		r = bufio.NewReaderSize(gz, 1<<20)
	}
	name := path
	if path == "-" {
		name = "stdin"
	}
	return &source{name: name, r: r, size: size, close: f.Close}, nil
}

type options struct {
	delim        rune // 0 = detect
	noHeader     bool
	decimalComma int // -1 auto, 0 off, 1 on
	maxRows      int64
	nulls        []string
}

type Profile struct {
	File         string    `json:"file"`
	Rows         int64     `json:"rows"`
	Delimiter    string    `json:"delimiter"`
	Bytes        int64     `json:"bytes,omitempty"`
	RaggedRows   int64     `json:"ragged_rows"`
	DecimalComma bool      `json:"decimal_comma"`
	Truncated    bool      `json:"truncated"`
	Columns      []*Column `json:"-"`
}

func profileReader(src *source, o options) (*Profile, error) {
	br, ok := src.r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(src.r)
	}
	sample, _ := br.Peek(64 << 10)
	sample = bytes.TrimPrefix(sample, []byte("\xef\xbb\xbf"))
	if bom, _ := br.Peek(3); bytes.Equal(bom, []byte("\xef\xbb\xbf")) {
		br.Discard(3)
	}
	d := o.delim
	if d == 0 {
		d = detectDelimiter(sample)
	}
	dec := o.decimalComma == 1 || o.decimalComma == -1 && d == ';'
	cr := csv.NewReader(br)
	cr.Comma = d
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true

	nulls := newNullSet(o.nulls)
	p := &Profile{File: src.name, Delimiter: delimName(d), Bytes: src.size, DecimalComma: dec}
	cr.ReuseRecord = false // records are handed to worker goroutines

	header, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return p, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src.name, err)
	}
	cols := make([]*Column, len(header))
	for i, h := range header {
		name := strings.TrimSpace(h)
		if o.noHeader || name == "" {
			name = fmt.Sprintf("col%d", i+1)
		}
		cols[i] = newColumn(name)
	}

	// Columns are independent, so they are split across workers; every worker sees every batch of rows
	// and updates only its own columns. The CSV reader runs in this goroutine.
	workers := min(runtime.GOMAXPROCS(0), len(cols))
	chans := make([]chan [][]string, workers)
	var wg sync.WaitGroup
	for w := range workers {
		chans[w] = make(chan [][]string, 4)
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for batch := range chans[w] {
				for ci := w; ci < len(cols); ci += workers {
					c := cols[ci]
					for _, rec := range batch {
						v := ""
						if ci < len(rec) {
							v = rec[ci]
						}
						c.add(v, dec, nulls)
					}
				}
			}
		}(w)
	}
	send := func(b [][]string) {
		for _, ch := range chans {
			ch <- b
		}
	}
	const batchSize = 2048
	batch := make([][]string, 0, batchSize)
	if o.noHeader {
		batch = append(batch, header)
		p.Rows++
	}
	var readErr error
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			readErr = fmt.Errorf("%s: %w", src.name, err)
			break
		}
		if o.maxRows > 0 && p.Rows >= o.maxRows {
			p.Truncated = true
			break
		}
		p.Rows++
		if len(rec) != len(cols) {
			p.RaggedRows++
		}
		batch = append(batch, rec)
		if len(batch) == batchSize {
			send(batch)
			batch = make([][]string, 0, batchSize)
		}
	}
	if len(batch) > 0 {
		send(batch)
	}
	for _, ch := range chans {
		close(ch)
	}
	wg.Wait()
	if readErr != nil {
		return nil, readErr
	}
	p.Columns = cols
	return p, nil
}

func delimName(d rune) string {
	switch d {
	case '\t':
		return "tab"
	case ',':
		return "comma"
	case ';':
		return "semicolon"
	case '|':
		return "pipe"
	}
	return string(d)
}
