package cursor

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"
	"time"
)

// Helper to build a minimal valid PNG image byte slice.
func buildTestPNG(w, h int, c color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// Helper to build a valid .cur file with a PNG or DIB payload.
func buildTestCUR(w, h, hotX, hotY int, payload []byte) []byte {
	var buf bytes.Buffer

	// ICONDIR
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0)) // idReserved
	_ = binary.Write(&buf, binary.LittleEndian, uint16(2)) // idType (2 = cursor)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // idCount (1 image)

	// ICONDIRENTRY
	bW := byte(w)
	if w >= 256 {
		bW = 0
	}
	bH := byte(h)
	if h >= 256 {
		bH = 0
	}
	buf.WriteByte(bW)
	buf.WriteByte(bH)
	buf.WriteByte(0) // bColorCount
	buf.WriteByte(0) // bReserved
	_ = binary.Write(&buf, binary.LittleEndian, uint16(hotX))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(hotY))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(payload)))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(6+16)) // offset to image data

	buf.Write(payload)
	return buf.Bytes()
}

// Helper to build an uncompressed 32bpp DIB payload.
func buildTestDIB32(w, h int) []byte {
	var buf bytes.Buffer
	// BITMAPINFOHEADER (40 bytes)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(40))    // biSize
	_ = binary.Write(&buf, binary.LittleEndian, int32(w))      // biWidth
	_ = binary.Write(&buf, binary.LittleEndian, int32(h*2))    // biHeight (x2 for DIB with mask)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))     // biPlanes
	_ = binary.Write(&buf, binary.LittleEndian, uint16(32))    // biBitCount
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))     // biCompression
	_ = binary.Write(&buf, binary.LittleEndian, uint32(w*h*4)) // biSizeImage
	_ = binary.Write(&buf, binary.LittleEndian, int32(0))      // biXPelsPerMeter
	_ = binary.Write(&buf, binary.LittleEndian, int32(0))      // biYPelsPerMeter
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))     // biClrUsed
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))     // biClrImportant

	// Pixel data: BGRA
	for range h {
		for range w {
			buf.Write([]byte{255, 0, 0, 255}) // Blue, Green 0, Red 0, Alpha 255
		}
	}
	// AND mask: 1bpp row padded to 32 bits (all 0 = fully opaque)
	maskRowBytes := ((w + 31) / 32) * 4
	buf.Write(make([]byte, maskRowBytes*h))

	return buf.Bytes()
}

// Helper to construct a synthetic .ani file.
func buildTestANI(frames [][]byte, rates []uint32, seq []uint32, w, h int) []byte {
	var listFram bytes.Buffer
	listFram.WriteString("fram")
	for _, f := range frames {
		listFram.WriteString("icon")
		_ = binary.Write(&listFram, binary.LittleEndian, uint32(len(f)))
		listFram.Write(f)
		if len(f)&1 != 0 {
			listFram.WriteByte(0)
		}
	}

	var riffBody bytes.Buffer
	riffBody.WriteString("ACON")

	// anih chunk
	riffBody.WriteString("anih")
	_ = binary.Write(&riffBody, binary.LittleEndian, uint32(36))
	_ = binary.Write(&riffBody, binary.LittleEndian, uint32(36))          // cbSize
	_ = binary.Write(&riffBody, binary.LittleEndian, uint32(len(frames))) // nFrames
	_ = binary.Write(&riffBody, binary.LittleEndian, uint32(len(frames))) // nSteps
	_ = binary.Write(&riffBody, binary.LittleEndian, uint32(w))           // iWidth
	_ = binary.Write(&riffBody, binary.LittleEndian, uint32(h))           // iHeight
	_ = binary.Write(&riffBody, binary.LittleEndian, uint32(32))          // nBitsPixel
	_ = binary.Write(&riffBody, binary.LittleEndian, uint32(1))           // nPlanes
	_ = binary.Write(&riffBody, binary.LittleEndian, uint32(6))           // iDispRate (6 jiffies = 100ms)
	_ = binary.Write(&riffBody, binary.LittleEndian, uint32(1))           // bfAttributes

	// rate chunk
	if len(rates) > 0 {
		riffBody.WriteString("rate")
		_ = binary.Write(&riffBody, binary.LittleEndian, uint32(len(rates)*4))
		for _, r := range rates {
			_ = binary.Write(&riffBody, binary.LittleEndian, r)
		}
	}

	// seq chunk
	if len(seq) > 0 {
		riffBody.WriteString("seq ")
		_ = binary.Write(&riffBody, binary.LittleEndian, uint32(len(seq)*4))
		for _, s := range seq {
			_ = binary.Write(&riffBody, binary.LittleEndian, s)
		}
	}

	// LIST fram chunk
	riffBody.WriteString("LIST")
	_ = binary.Write(&riffBody, binary.LittleEndian, uint32(listFram.Len()))
	riffBody.Write(listFram.Bytes())
	if listFram.Len()&1 != 0 {
		riffBody.WriteByte(0)
	}

	var result bytes.Buffer
	result.WriteString("RIFF")
	_ = binary.Write(&result, binary.LittleEndian, uint32(riffBody.Len()))
	result.Write(riffBody.Bytes())

	return result.Bytes()
}

func TestFormatDetection(t *testing.T) {
	pngData := buildTestPNG(16, 16, color.RGBA{R: 255, A: 255})
	curData := buildTestCUR(16, 16, 2, 3, pngData)
	aniData := buildTestANI([][]byte{curData}, nil, nil, 16, 16)

	if !IsCUR(curData) {
		t.Errorf("IsCUR failed to detect valid CUR")
	}
	if IsANI(curData) {
		t.Errorf("IsANI false positive on CUR data")
	}
	if !IsCursor(curData) {
		t.Errorf("IsCursor failed on valid CUR")
	}

	if !IsANI(aniData) {
		t.Errorf("IsANI failed to detect valid ANI")
	}
	if IsCUR(aniData) {
		t.Errorf("IsCUR false positive on ANI data")
	}
	if !IsCursor(aniData) {
		t.Errorf("IsCursor failed on valid ANI")
	}

	invalid := []byte("not a cursor file at all")
	if IsCursor(invalid) || IsCUR(invalid) || IsANI(invalid) {
		t.Errorf("IsCursor returned true on invalid input")
	}
}

func TestDecodeCUR_PNG(t *testing.T) {
	pngData := buildTestPNG(32, 32, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	curData := buildTestCUR(32, 32, 5, 8, pngData)

	c, err := DecodeBytes(curData)
	if err != nil {
		t.Fatalf("DecodeBytes failed: %v", err)
	}

	if c.Animated {
		t.Errorf("expected static cursor, got animated")
	}
	if len(c.Frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(c.Frames))
	}
	if c.HotspotX != 5 || c.HotspotY != 8 {
		t.Errorf("expected hotspot (5, 8), got (%d, %d)", c.HotspotX, c.HotspotY)
	}
	if c.Width != 32 || c.Height != 32 {
		t.Errorf("expected dimensions 32x32, got %dx%d", c.Width, c.Height)
	}
}

func TestDecodeCUR_DIB(t *testing.T) {
	dibData := buildTestDIB32(16, 16)
	curData := buildTestCUR(16, 16, 1, 2, dibData)

	c, err := DecodeBytes(curData)
	if err != nil {
		t.Fatalf("DecodeBytes failed: %v", err)
	}

	if len(c.Frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(c.Frames))
	}
	if c.HotspotX != 1 || c.HotspotY != 2 {
		t.Errorf("expected hotspot (1, 2), got (%d, %d)", c.HotspotX, c.HotspotY)
	}
	bounds := c.Frames[0].Bounds()
	if bounds.Dx() != 16 || bounds.Dy() != 16 {
		t.Errorf("expected bounds 16x16, got %dx%d", bounds.Dx(), bounds.Dy())
	}
}

func TestDecodeANI(t *testing.T) {
	pngFrame1 := buildTestPNG(16, 16, color.RGBA{R: 255, A: 255})
	pngFrame2 := buildTestPNG(16, 16, color.RGBA{G: 255, A: 255})

	cur1 := buildTestCUR(16, 16, 3, 4, pngFrame1)
	cur2 := buildTestCUR(16, 16, 3, 4, pngFrame2)

	// 12 jiffies = ~200ms
	rates := []uint32{12, 6}
	aniData := buildTestANI([][]byte{cur1, cur2}, rates, []uint32{0, 1}, 16, 16)

	c, err := Decode(bytes.NewReader(aniData))
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if !c.Animated {
		t.Errorf("expected animated cursor, got static")
	}
	if len(c.Frames) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(c.Frames))
	}
	if c.HotspotX != 3 || c.HotspotY != 4 {
		t.Errorf("expected hotspot (3, 4), got (%d, %d)", c.HotspotX, c.HotspotY)
	}
	if len(c.DelayMs) != 2 {
		t.Fatalf("expected 2 delays, got %d", len(c.DelayMs))
	}
	if c.DelayMs[0] != 200 || c.DelayMs[1] != 100 {
		t.Errorf("expected delays [200, 100], got %v", c.DelayMs)
	}
	if c.Delays[0] != 200*time.Millisecond {
		t.Errorf("expected duration 200ms, got %v", c.Delays[0])
	}
}

func TestHotspotFraction(t *testing.T) {
	c := &Cursor{
		HotspotX: 16,
		HotspotY: 24,
		Width:    32,
		Height:   32,
	}

	// Without crop: 16/32 = 0.5, 24/32 = 0.75
	hx, hy := c.HotspotFraction(0, 0, 0, 0)
	if hx != 0.5 || hy != 0.75 {
		t.Errorf("expected (0.5, 0.75), got (%v, %v)", hx, hy)
	}

	// With crop: crop bounds [10, 10, 20, 20]
	// hx = (16 - 10) / 20 = 6/20 = 0.3
	// hy = (24 - 10) / 20 = 14/20 = 0.7
	hx, hy = c.HotspotFraction(10, 10, 20, 20)
	if math.Abs(hx-0.3) > 1e-6 || math.Abs(hy-0.7) > 1e-6 {
		t.Errorf("expected (0.3, 0.7), got (%v, %v)", hx, hy)
	}

	// Clamping outside crop bounds
	hx, hy = c.HotspotFraction(20, 30, 10, 10)
	if hx != 0.0 || hy != 0.0 {
		t.Errorf("expected clamped (0.0, 0.0), got (%v, %v)", hx, hy)
	}
}

func TestCorruptedInput(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"random bytes", []byte("foobarbaz123456789")},
		{"truncated CUR header", []byte{0, 0, 2, 0}},
		{"truncated CUR entry", []byte{0, 0, 2, 0, 1, 0, 10}},
		{"truncated ANI header", []byte("RIFF\x0c\x00\x00\x00ACON")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeBytes(tc.data)
			if err == nil {
				t.Errorf("expected error decoding %s, got nil", tc.name)
			}
		})
	}
}

func BenchmarkDecodeCUR(b *testing.B) {
	pngData := buildTestPNG(32, 32, color.RGBA{R: 100, G: 150, B: 200, A: 255})
	curData := buildTestCUR(32, 32, 10, 10, pngData)

	for b.Loop() {
		_, err := DecodeBytes(curData)
		if err != nil {
			b.Fatalf("DecodeBytes failed: %v", err)
		}
	}
}
