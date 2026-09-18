# go-cursor

[![Go Reference](https://pkg.go.dev/badge/github.com/fumbledlol/go-cursor.svg)](https://pkg.go.dev/github.com/fumbledlol/go-cursor)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Go library for parsing and decoding Windows static (`.cur`) and animated (`.ani`) cursor files.

## Features

- Windows static cursors (`.cur` ICONDIR resource type 2)
- Windows animated cursors (`.ani` RIFF ACON containers with `anih`, `LIST/fram`, `rate`, and `seq` chunks)
- Image decoding for 32-bit DIB, 24-bit DIB with 1-bpp AND mask transparency, and PNG-compressed cursors
- Pixel hotspot coordinates and normalized fractional coordinates with crop bounds (`HotspotFraction`)
- Frame delays converted from Windows jiffies (1/60s) into `time.Duration` and millisecond delays
- Bounds checks against malformed files and memory bombs
- Standard library only, no CGO or external image libraries

## Installation

```bash
go get github.com/fumbledlol/go-cursor
```

## Quick start

### Decoding a cursor

```go
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/fumbledlol/go-cursor"
)

func main() {
	file, err := os.Open("custom.ani")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	cur, err := cursor.Decode(file)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Dimensions: %dx%d\n", cur.Width, cur.Height)
	fmt.Printf("Hotspot: (%d, %d)\n", cur.HotspotX, cur.HotspotY)
	fmt.Printf("Animated: %v (Frames: %d)\n", cur.Animated, len(cur.Frames))

	for i, delay := range cur.Delays {
		fmt.Printf("  Frame %d: delay %v\n", i, delay)
	}

	// Normalized fractional hotspot survives image resizing:
	hx, hy := cur.HotspotFraction(0, 0, 0, 0)
	fmt.Printf("Normalized hotspot: (%.2f, %.2f)\n", hx, hy)
}
```

### Checking file format

```go
data, _ := os.ReadFile("unknown.dat")

if cursor.IsCursor(data) {
	if cursor.IsANI(data) {
		fmt.Println("Windows animated cursor (.ani)")
	} else if cursor.IsCUR(data) {
		fmt.Println("Windows static cursor (.cur)")
	}
}
```

## Benchmarks

```
BenchmarkDecodeCUR-12    85923    16738 ns/op
```

## License

[MIT](LICENSE)