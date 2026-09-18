// Package cursor provides a pure Go parser and decoder for Windows static (.cur)
// and animated (.ani) cursor files, extracting animation frames, per-frame
// durations, and pixel-precise cursor hotspots.
package cursor

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"time"
)

const (
	maxImageDimension  = 4096
	maxImagePixels     = 4096 * 4096
	maxAnimationFrames = 256
)

var (
	// ErrUnsupportedFormat indicates the input data is neither a valid .cur nor .ani file.
	ErrUnsupportedFormat = errors.New("unsupported cursor format: only .cur and .ani files are supported")
	// ErrTruncatedData indicates the input stream terminated prematurely.
	ErrTruncatedData = errors.New("truncated cursor data")
	// ErrInvalidHeader indicates malformed header metadata.
	ErrInvalidHeader = errors.New("invalid cursor header")
	// ErrNoFrames indicates the cursor file contains no decodable image frames.
	ErrNoFrames = errors.New("cursor file contains no frames")
	// ErrFrameLimitExceeded indicates an animation exceeded the maximum allowed frames.
	ErrFrameLimitExceeded = errors.New("animation frame limit exceeded")
)

// Cursor represents a decoded Windows static or animated cursor.
type Cursor struct {
	// Frames contains each decoded image frame (NRGBA or RGBA).
	Frames []image.Image
	// Delays contains the display duration for each frame.
	Delays []time.Duration
	// DelayMs contains the display duration in integer milliseconds for each frame.
	DelayMs []int
	// HotspotX is the raw pixel X coordinate of the cursor active point.
	HotspotX int
	// HotspotY is the raw pixel Y coordinate of the cursor active point.
	HotspotY int
	// Width is the nominal width of the cursor in pixels.
	Width int
	// Height is the nominal height of the cursor in pixels.
	Height int
	// Animated reports whether this cursor contains multiple animation frames (.ani).
	Animated bool
}

// IsCursor reports whether the given data starts with a valid .cur or .ani header.
func IsCursor(data []byte) bool {
	return IsCUR(data) || IsANI(data)
}

// IsCUR reports whether the given data begins with an ICONDIR header configured
// for Windows cursor resources (resource type 2).
func IsCUR(data []byte) bool {
	return len(data) >= 6 && data[0] == 0 && data[1] == 0 && data[2] == 2 && data[3] == 0
}

// IsANI reports whether the given data begins with the RIFF ACON container header.
func IsANI(data []byte) bool {
	return len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "ACON"
}

// Decode reads cursor data from an io.Reader and returns the decoded Cursor.
func Decode(r io.Reader) (*Cursor, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read cursor data: %w", err)
	}
	return DecodeBytes(data)
}

// DecodeBytes parses cursor data from a byte slice and returns the decoded Cursor.
func DecodeBytes(data []byte) (*Cursor, error) {
	if IsANI(data) {
		return decodeANI(data)
	}
	if IsCUR(data) {
		return decodeCUR(data)
	}
	return nil, ErrUnsupportedFormat
}

// HotspotFraction converts the raw pixel hotspot into normalized [0.0, 1.0] fractions
// relative to a given crop rectangle. If crop dimensions are not positive,
// fractions are computed relative to the cursor nominal source dimensions.
// Normalized fractions survive scaling and resizing operations.
func (c *Cursor) HotspotFraction(cropX, cropY, cropW, cropH int) (float64, float64) {
	if cropW > 0 && cropH > 0 {
		hx := float64(c.HotspotX-cropX) / float64(cropW)
		hy := float64(c.HotspotY-cropY) / float64(cropH)
		return clamp01(hx), clamp01(hy)
	}
	if c.Width > 0 && c.Height > 0 {
		return clamp01(float64(c.HotspotX) / float64(c.Width)), clamp01(float64(c.HotspotY) / float64(c.Height))
	}
	return 0.5, 0.5
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func jiffiesToMs(j uint32) int {
	return int(math.Round(float64(j) * 1000.0 / 60.0))
}

// decodeCUR decodes a static Windows cursor file (.cur).
func decodeCUR(data []byte) (*Cursor, error) {
	img, hotX, hotY, err := parseIcoEntry(data)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	defaultDelay := 100 * time.Millisecond
	return &Cursor{
		Frames:   []image.Image{img},
		Delays:   []time.Duration{defaultDelay},
		DelayMs:  []int{100},
		HotspotX: hotX,
		HotspotY: hotY,
		Width:    w,
		Height:   h,
		Animated: false,
	}, nil
}

type aniHeader struct {
	CbSize       uint32
	NFrames      uint32
	NSteps       uint32
	IWidth       uint32
	IHeight      uint32
	NBitsPixel   uint32
	NPlanes      uint32
	IDispRate    uint32
	BfAttributes uint32
}

// decodeANI decodes an animated Windows cursor (.ani).
func decodeANI(data []byte) (*Cursor, error) {
	var hdr aniHeader
	var hasHdr bool
	var rates []uint32
	var seq []uint32
	var icons [][]byte

	pos := 12 // RIFF (4) + size (4) + ACON (4)
	end := len(data)
	for pos+8 <= end {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		if size < 0 || pos+8+size > end {
			break
		}
		chunk := data[pos+8 : pos+8+size]
		switch id {
		case "anih":
			if len(chunk) >= 36 {
				hdr = aniHeader{
					CbSize:       binary.LittleEndian.Uint32(chunk[0:4]),
					NFrames:      binary.LittleEndian.Uint32(chunk[4:8]),
					NSteps:       binary.LittleEndian.Uint32(chunk[8:12]),
					IWidth:       binary.LittleEndian.Uint32(chunk[12:16]),
					IHeight:      binary.LittleEndian.Uint32(chunk[16:20]),
					NBitsPixel:   binary.LittleEndian.Uint32(chunk[20:24]),
					NPlanes:      binary.LittleEndian.Uint32(chunk[24:28]),
					IDispRate:    binary.LittleEndian.Uint32(chunk[28:32]),
					BfAttributes: binary.LittleEndian.Uint32(chunk[32:36]),
				}
				hasHdr = true
			}
		case "rate":
			for i := 0; i+4 <= len(chunk); i += 4 {
				rates = append(rates, binary.LittleEndian.Uint32(chunk[i:i+4]))
			}
		case "seq":
			for i := 0; i+4 <= len(chunk); i += 4 {
				seq = append(seq, binary.LittleEndian.Uint32(chunk[i:i+4]))
			}
		case "LIST":
			if len(chunk) >= 4 && string(chunk[0:4]) == "fram" {
				p := 4
				for p+8 <= len(chunk) {
					sid := string(chunk[p : p+4])
					ssize := int(binary.LittleEndian.Uint32(chunk[p+4 : p+8]))
					if ssize < 0 || p+8+ssize > len(chunk) {
						break
					}
					if sid == "icon" {
						icons = append(icons, chunk[p+8:p+8+ssize])
					}
					p += 8 + ssize + (ssize & 1)
				}
			}
		}
		pos += 8 + size + (size & 1)
	}

	if !hasHdr {
		return nil, fmt.Errorf("%w: missing anih chunk", ErrInvalidHeader)
	}
	if hdr.NFrames == 0 {
		return nil, ErrNoFrames
	}
	if len(icons) == 0 {
		return nil, fmt.Errorf("%w: no icon chunks found", ErrNoFrames)
	}

	order := make([]int, 0, len(icons))
	if len(seq) > 0 {
		for _, s := range seq {
			if int(s) < len(icons) {
				order = append(order, int(s))
			}
		}
	}
	if len(order) == 0 {
		for i := 0; i < len(icons); i++ {
			order = append(order, i)
		}
	}

	if len(order) > maxAnimationFrames {
		return nil, fmt.Errorf("%w: %d frames exceed maximum %d", ErrFrameLimitExceeded, len(order), maxAnimationFrames)
	}

	defaultMs := 100
	if hdr.IDispRate > 0 {
		defaultMs = jiffiesToMs(hdr.IDispRate)
	}
	perFrame := make([]int, len(order))
	for i := range order {
		perFrame[i] = defaultMs
	}
	for i := 0; i < len(order) && i < len(rates); i++ {
		if rates[i] > 0 {
			perFrame[i] = jiffiesToMs(rates[i])
		}
	}

	frames := make([]image.Image, 0, len(order))
	delays := make([]time.Duration, 0, len(order))
	delayMs := make([]int, 0, len(order))
	var hotX, hotY int

	for fi, idx := range order {
		img, fHotX, fHotY, err := parseIcoEntry(icons[idx])
		if err != nil {
			return nil, err
		}
		if fi == 0 {
			hotX, hotY = fHotX, fHotY
		}
		frames = append(frames, img)
		delays = append(delays, time.Duration(perFrame[fi])*time.Millisecond)
		delayMs = append(delayMs, perFrame[fi])
	}

	srcW, srcH := int(hdr.IWidth), int(hdr.IHeight)
	if srcW <= 0 || srcH <= 0 {
		b := frames[0].Bounds()
		srcW, srcH = b.Dx(), b.Dy()
	}

	return &Cursor{
		Frames:   frames,
		Delays:   delays,
		DelayMs:  delayMs,
		HotspotX: hotX,
		HotspotY: hotY,
		Width:    srcW,
		Height:   srcH,
		Animated: len(frames) > 1,
	}, nil
}

// parseIcoEntry parses an ICONDIR / CUR structure and returns the largest image entry.
func parseIcoEntry(payload []byte) (image.Image, int, int, error) {
	if len(payload) < 6 {
		return nil, 0, 0, ErrTruncatedData
	}
	typ := binary.LittleEndian.Uint16(payload[2:4])
	count := int(binary.LittleEndian.Uint16(payload[4:6]))
	if count == 0 || len(payload) < 6+16*count {
		return nil, 0, 0, fmt.Errorf("%w: invalid icon count %d", ErrInvalidHeader, count)
	}

	bestIdx := -1
	bestArea := 0
	for i := range count {
		off := 6 + i*16
		bWidth := int(payload[off])
		bHeight := int(payload[off+1])
		w, h := bWidth, bHeight
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		if w*h > bestArea {
			bestArea = w * h
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return nil, 0, 0, ErrNoFrames
	}

	off := 6 + bestIdx*16
	var hotX, hotY int
	if typ == 2 {
		hotX = int(binary.LittleEndian.Uint16(payload[off+4 : off+6]))
		hotY = int(binary.LittleEndian.Uint16(payload[off+6 : off+8]))
	}
	size := int(binary.LittleEndian.Uint32(payload[off+8 : off+12]))
	imgOff := int(binary.LittleEndian.Uint32(payload[off+12 : off+16]))
	if imgOff < 0 || size <= 0 || imgOff+size > len(payload) {
		return nil, 0, 0, fmt.Errorf("%w: icon image offset out of range", ErrInvalidHeader)
	}

	img, err := decodeIconImage(payload[imgOff : imgOff+size])
	if err != nil {
		return nil, 0, 0, err
	}
	return img, hotX, hotY, nil
}

// decodeIconImage handles both modern PNG-compressed icon entries and classic uncompressed DIBs.
func decodeIconImage(payload []byte) (image.Image, error) {
	if len(payload) >= 8 && string(payload[0:8]) == "\x89PNG\r\n\x1a\n" {
		img, err := png.Decode(bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("failed to decode icon PNG: %w", err)
		}
		return img, nil
	}
	return decodeIconDIB(payload)
}

// decodeIconDIB parses BITMAPINFOHEADER and pixel rows with 1-bpp AND masks for transparency.
func decodeIconDIB(payload []byte) (image.Image, error) {
	if len(payload) < 40 {
		return nil, ErrTruncatedData
	}
	biSize := int(binary.LittleEndian.Uint32(payload[0:4]))
	if biSize < 40 || biSize > len(payload) {
		return nil, fmt.Errorf("%w: unsupported DIB header size %d", ErrInvalidHeader, biSize)
	}
	w := int(int32(binary.LittleEndian.Uint32(payload[4:8])))     // #nosec G115 -- biWidth is signed 32-bit integer per BMP spec
	rawH := int(int32(binary.LittleEndian.Uint32(payload[8:12]))) // #nosec G115 -- biHeight is signed 32-bit integer per BMP spec
	bitCount := binary.LittleEndian.Uint16(payload[14:16])
	compression := binary.LittleEndian.Uint32(payload[16:20])

	if w <= 0 || w > maxImageDimension || rawH == 0 {
		return nil, fmt.Errorf("%w: invalid DIB dimensions %dx%d", ErrInvalidHeader, w, rawH)
	}

	colorH := rawH
	if colorH < 0 {
		colorH = -colorH
	}
	hasMask := rawH > 0
	if hasMask {
		colorH = rawH / 2
	}
	topDown := rawH < 0
	if colorH <= 0 || colorH > maxImageDimension || w*colorH > maxImagePixels {
		return nil, fmt.Errorf("%w: invalid DIB dimensions %dx%d", ErrInvalidHeader, w, colorH)
	}
	if compression != 0 {
		return nil, fmt.Errorf("%w: unsupported compression %d", ErrInvalidHeader, compression)
	}

	pixels := payload[biSize:]
	img := image.NewNRGBA(image.Rect(0, 0, w, colorH))
	dstY := func(y int) int {
		if topDown {
			return y
		}
		return colorH - 1 - y
	}

	switch bitCount {
	case 32:
		rowBytes := w * 4
		for y := 0; y < colorH; y++ {
			row := y * rowBytes
			if row+rowBytes > len(pixels) {
				return nil, ErrTruncatedData
			}
			for x := range w {
				i := row + x*4
				img.SetNRGBA(x, dstY(y), color.NRGBA{
					R: pixels[i+2],
					G: pixels[i+1],
					B: pixels[i],
					A: pixels[i+3],
				})
			}
		}
	case 24:
		rowBytes := ((w*3 + 3) / 4) * 4
		for y := 0; y < colorH; y++ {
			row := y * rowBytes
			if row+rowBytes > len(pixels) {
				return nil, ErrTruncatedData
			}
			for x := range w {
				i := row + x*3
				img.SetNRGBA(x, dstY(y), color.NRGBA{
					R: pixels[i+2],
					G: pixels[i+1],
					B: pixels[i],
					A: 255,
				})
			}
		}
		if hasMask {
			maskStart := colorH * rowBytes
			maskRowBytes := ((w + 31) / 32) * 4
			for y := 0; y < colorH; y++ {
				row := maskStart + y*maskRowBytes
				if row+maskRowBytes > len(pixels) {
					break
				}
				for x := range w {
					if pixels[row+x/8]&(0x80>>(uint(x)%8)) != 0 {
						img.SetNRGBA(x, dstY(y), color.NRGBA{})
					}
				}
			}
		}
	default:
		return nil, fmt.Errorf("%w: unsupported bit depth %d", ErrInvalidHeader, bitCount)
	}
	return img, nil
}
