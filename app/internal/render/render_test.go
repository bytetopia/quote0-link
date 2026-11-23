package render

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/bytetopia/quote0-link/app/internal/device"
)

func encodedPNG(t *testing.T, img image.Image) string {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buffer.Bytes())
}

func whiteRatio(frame device.Frame) float64 {
	white := 0
	for _, value := range frame {
		for bit := 0; bit < 8; bit++ {
			if value&(0x80>>bit) != 0 {
				white++
			}
		}
	}
	return float64(white) / float64(device.Width*device.Height)
}

func TestImageDithersMidtones(t *testing.T) {
	renderer, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	for _, tone := range []uint8{64, 128, 192} {
		source := image.NewRGBA(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				source.Set(x, y, color.RGBA{tone, tone, tone, 255})
			}
		}
		frame, err := renderer.Draw(Card{Template: "image", Image: encodedPNG(t, source)})
		if err != nil {
			t.Fatal(err)
		}
		ratio, want := whiteRatio(frame), float64(tone)/255
		if ratio < want-0.03 || ratio > want+0.03 {
			t.Errorf("tone %d: white ratio %.3f, want about %.3f", tone, ratio, want)
		}
	}
}

func TestSaturatedColorsStayVisible(t *testing.T) {
	renderer, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	// Pure red and blue were previously flattened to solid black.
	for _, fill := range []color.RGBA{{255, 0, 0, 255}, {0, 0, 255, 255}, {0, 160, 0, 255}} {
		source := image.NewRGBA(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				source.Set(x, y, fill)
			}
		}
		frame, err := renderer.Draw(Card{Template: "image", Image: encodedPNG(t, source)})
		if err != nil {
			t.Fatal(err)
		}
		if ratio := whiteRatio(frame); ratio < 0.05 || ratio > 0.95 {
			t.Errorf("color %v: white ratio %.3f is not a dithered tone", fill, ratio)
		}
	}
}

func TestBundledCJKFont(t *testing.T) {
	renderer, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	if renderer.fallback == nil {
		t.Fatal("bundled CJK font not loaded")
	}
	frame, err := renderer.Draw(Card{Template: "text", Body: "今日天气晴"})
	if err != nil {
		t.Fatal(err)
	}
	if ratio := whiteRatio(frame); ratio > 0.99 {
		t.Errorf("CJK glyphs missing: white ratio %.4f", ratio)
	}
}

func TestTextStaysCrisp(t *testing.T) {
	renderer, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	frame, err := renderer.Draw(Card{Template: "text", Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if ratio := whiteRatio(frame); ratio < 0.95 {
		t.Errorf("text frame unexpectedly dark: %.3f", ratio)
	}
}
