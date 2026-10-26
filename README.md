# csvglance

[![CI](https://github.com/celaldrg/csvglance/actions/workflows/ci.yml/badge.svg)](https://github.com/celaldrg/csvglance/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/celaldrg/csvglance?sort=semver)](https://github.com/celaldrg/csvglance/releases)
![Go](https://img.shields.io/badge/go-1.24%2B-00ADD8)
![No dependencies](https://img.shields.io/badge/dependencies-stdlib%20only-brightgreen)

**Know a CSV file before you load it.** `csvglance` reads a CSV in one streaming pass and tells you, for every column,
the **type**, how much is **missing**, how many **distinct** values there are, the **range**, the **mean** and the
**most common values**. It also points out the problems that break imports: numbers with a few junk values, ragged rows,
empty columns.

A single static binary. It detects the delimiter (`,` `;` tab `|`), handles gzip, UTF-8 BOM and decimal commas,
and spreads the work across CPU cores. A million rows takes about a second.

## Example

Real output on the [OurAirports airport-codes dataset](https://github.com/datasets/airport-codes) (`data/airport-codes.csv`, commit `0a775c0`):

```text
$ csvglance airport-codes.csv
airport-codes.csv · 86,286 rows · 13 columns · comma-separated · 8.4 MB · 87ms

#   COLUMN        TYPE    FILLED  DISTINCT  RANGE            MEAN     TOP VALUES
1   ident         string  100%    86,286    len 3 … 8
2   type          string  100%    7         len 6 … 14                small_airport (42.8k), heliport (23.3k), closed (13.6k)
3   name          string  100%    81,681    len 2 … 108               Centre Hospitalie… (49), [Duplicate] Mukho… (47), Fazenda Santa Mar… (28)
4   elevation_ft  int     82.6%   6,485     -1,266 … 17,372  1,290.9  10 (597), 20 (469), 3 (366)
5   continent     string  100%    7         len 2                     NA (39.8k), AS (12.8k), EU (12.6k)
6   iso_country   string  100%    247       len 2                     US (32.7k), BR (8,113), JP (3,747)
7   iso_region    string  100%    3,019     len 4 … 7                 US-TX (3,893), US-CA (2,431), US-FL (1,376)
8   municipality  string  94.4%   38,446    len 2 … 61                Osaka (420), Seoul (416), São Paulo (281)
9   icao_code     string  12.3%   10,616    len 4
10  iata_code     string  10.4%   9,051     len 3
11  gps_code      string  51.5%   44,500    len 3 … 4
12  local_code    string  41.8%   34,643    len 3 … 7                 AMA (5), HSJ (5), LAN (5)
13  coordinates   string  100%    86,152    len 6 … 41                37.540345, 129.11… (48), 38.247439, 127.87… (16), 37.56984, 126.982… (4)

• ident is unique and never empty: a good key
```

Note `continent`: **`NA` is North America here**, and in `iso_country` it is Namibia. Many tools silently read `NA` as missing.
csvglance does not, unless you ask for it with `-null NA`.

### A European/Turkish style file

Semicolon-separated, decimal commas, `dd.mm.yyyy` dates and zero-padded codes. This is the sample file in
[`examples/siparisler.csv`](examples/siparisler.csv) (generated test data):

```text
$ csvglance examples/siparisler.csv
examples/siparisler.csv · 500 rows · 7 columns · semicolon-separated · decimal comma · 23.3 KB · 1ms

#  COLUMN      TYPE    FILLED  DISTINCT  RANGE                    MEAN       TOP VALUES
1  siparis_no  int     100%    500       100,001 … 100,500        100,250.5
2  tarih       date    100%    234       2026-01-01 … 2026-10-18             18.10.2026 (7), 10.09.2026 (6), 21.02.2026 (6)
3  il          string  100%    8         len 5 … 9                           İstanbul (164), Ankara (81), İzmir (61)
4  plaka       string  100%    8         len 2                               34 (164), 06 (81), 35 (61)
5  tutar       string  100%    499       len 5 … 8                           belirsiz (2)
6  kargo       string  80.4%   4         len 3 … 7                           PTT (108), Aras (107), MNG (99)
7  iade        string  100%    2         len 4 … 5                           hayır (371), evet (129)

• siparis_no is unique and never empty: a good key
• tutar looks like float but 2 value(s) are not, e.g. "belirsiz"
```

`plaka` stays a string because `06` is a code, not the number 6. And the last line shows why the `tutar` (amount)
column would fail a numeric import.

### Speed

The airport file repeated 12 times (1,035,432 rows, 101 MB) on an 8-core machine:

```text
$ csvglance airports-x12.csv
airports-x12.csv · 1,035,432 rows · 13 columns · comma-separated · 101.0 MB · 867ms
```

### JSON

```text
$ csvglance -json -top 2 airport-codes.csv.gz      # gzip is detected automatically
{
  "file": "airport-codes.csv.gz",
  "rows": 86286,
  "delimiter": "comma",
  ...
  "columns": [
    ...
    {
      "name": "elevation_ft",
      "type": "int",
      "filled": 71316,
      "nulls": 14970,
      "distinct": 6485,
      "distinct_exact": true,
      "min": -1266,
      "max": 17372,
      "mean": 1290.9450754388936,
      "stddev": 1654.8838665739972,
      "top": [
        { "value": "10", "count": 597 },
        { "value": "20", "count": 469 }
      ]
    },
    ...
```

## Install

```sh
go install github.com/celaldrg/csvglance@latest
```

Or download a binary for Linux, macOS or Windows from the [releases](https://github.com/celaldrg/csvglance/releases).

## Usage

```text
csvglance [flags] file.csv [more.csv.gz ...]      use - for stdin

  -d string        delimiter: , ; tab | (default: detect)
  -decimal string  decimal separator: auto (comma when delimiter is ;), dot, comma (default "auto")
  -json            JSON output
  -n int           read at most n data rows (0 = all)
  -no-header       first row is data, not column names
  -null string     extra comma-separated values to treat as missing, e.g. NA,-,?
  -top int         top values to show per column (default 3)
```

Exit codes: `0` ok, `1` a file could not be read or parsed, `2` usage error.

## How it works

- **One pass, bounded memory.** Distinct values and their counts are tracked exactly up to 100,000 per column.
  Above that, distinct counts switch to a [HyperLogLog](https://en.wikipedia.org/wiki/HyperLogLog) sketch
  (16 KB, ~0.8% error, shown as `~`) and top values to the
  [Space-Saving](https://doi.org/10.1007/978-3-540-30570-5_27) algorithm (Metwally et al., 2005), seeded with the exact counts
  so far. Its counts are guaranteed lower bounds, shown as `≥`.
- **Parallel.** The CSV reader feeds batches of rows to worker goroutines, and each worker owns a subset of the columns.
  No locks on the hot path.
- **Types** are the most specific one that fits every non-empty value: `bool`, `int`, `float`, `date`, `datetime`, `string`.
  Zero-padded values (`007`, `06`) stay strings. `Inf`, `NaN`, hex and `1_000` are not numbers. If at least 95% of a
  column fits a numeric or date type, csvglance tells you how many values do not, with an example.
- **Missing values:** empty, `null`, `none`, `nan`, `n/a`, `#n/a`, `nil` (any case). Add your own with `-null`.
- **Mean and standard deviation** use Welford's algorithm, which stays numerically stable on long columns.

## Development

```sh
go test -race ./...
go vet ./...
```

## License

[MIT](LICENSE) © Celalettin Durğaç
