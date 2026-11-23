package render

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strings"

	"github.com/bytetopia/quote0-link/app/internal/device"
	scale "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const width, height = device.Height, device.Width

type Card struct {
	Template string `json:"template"`
	Title    string `json:"title,omitempty"`
	Subtitle string `json:"subtitle,omitempty"`
	Body     string `json:"body,omitempty"`
	Image    string `json:"image_base64,omitempty"`
}

type Renderer struct {
	font     *opentype.Font
	fallback *opentype.Font
}

// cjkFont is bundled so CJK text renders identically on every OS instead of
// depending on whichever system font happens to be installed.
//
//go:embed fonts/NotoSansSC-Medium.otf
var cjkFont []byte

// New builds a renderer. An empty fontPath uses the bundled Noto Sans SC for
// non-ASCII text; a non-empty path overrides it with a TTF, OTF or TTC file.
func New(fontPath string) (Renderer, error) {
	regular, err := opentype.Parse(goregular.TTF)
	renderer := Renderer{font: regular}
	if err != nil {
		return renderer, err
	}
	if fontPath == "" {
		renderer.fallback, err = opentype.Parse(cjkFont)
		return renderer, err
	}
	data, err := os.ReadFile(fontPath)
	if err != nil {
		return renderer, err
	}
	collection, err := opentype.ParseCollection(data)
	if err != nil {
		return renderer, err
	}
	renderer.fallback, err = collection.Font(0)
	return renderer, err
}

func (renderer Renderer) Draw(card Card) (device.Frame, error) {
	canvas := image.NewGray(image.Rect(0, 0, width, height))
	draw.Draw(canvas, canvas.Bounds(), image.White, image.Point{}, draw.Src)
	switch card.Template {
	case "test":
		for row := 0; row < height; row++ {
			for col := 0; col < width; col++ {
				if (row/16+col/16)%2 == 0 {
					canvas.SetGray(col, row, color.Gray{})
				}
			}
		}
	case "text":
		if err := renderer.text(canvas, card.Body, 24, 12, 31, 4); err != nil {
			return device.Frame{}, err
		}
	case "card":
		draw.Draw(canvas, image.Rect(12, 64, width-12, 66), image.Black, image.Point{}, draw.Src)
		for _, line := range []struct {
			value         string
			size, y, rows int
		}{{card.Title, 23, 30, 1}, {card.Subtitle, 14, 53, 1}, {card.Body, 19, 92, 3}} {
			if err := renderer.text(canvas, line.value, line.size, 12, line.y, line.rows); err != nil {
				return device.Frame{}, err
			}
		}
	case "image":
		if err := paintImage(canvas, card.Image); err != nil {
			return device.Frame{}, err
		}
	default:
		return device.Frame{}, errors.New("unknown template; use text, card, image or test")
	}
	return pack(canvas), nil
}

func (renderer Renderer) text(canvas *image.Gray, text string, size, x, y, rows int) error {
	if len(text) > 4096 {
		return errors.New("text field exceeds 4096 bytes")
	}
	selected := renderer.font
	if renderer.fallback != nil && strings.IndexFunc(text, func(value rune) bool { return value > 127 }) >= 0 {
		selected = renderer.fallback
	}
	face, err := opentype.NewFace(selected, &opentype.FaceOptions{Size: float64(size), DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return err
	}
	defer face.Close()
	// Glyphs go into a coverage mask first so thin anti-aliased strokes can be
	// kept with a lower cut-off than the 50% threshold used by pack.
	mask := image.NewAlpha(canvas.Bounds())
	defer inkMask(canvas, mask)
	drawer := font.Drawer{Dst: mask, Src: image.Opaque, Face: face}
	line := ""
	for _, value := range text {
		next := line + string(value)
		if value == '\n' || (line != "" && drawer.MeasureString(next).Ceil() > width-2*x) {
			drawer.Dot = fixed.P(x, y)
			drawer.DrawString(line)
			rows--
			if rows == 0 {
				return nil
			}
			y += size + 4
			line = ""
		}
		if value != '\n' {
			line += string(value)
		}
	}
	drawer.Dot = fixed.P(x, y)
	drawer.DrawString(line)
	return nil
}

func paintImage(canvas *image.Gray, encoded string) error {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return err
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 {
		return errors.New("image dimensions exceed 4096 pixels")
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	ratio := min(float64(width)/float64(config.Width), float64(height)/float64(config.Height))
	targetWidth, targetHeight := max(1, int(float64(config.Width)*ratio)), max(1, int(float64(config.Height)*ratio))
	offsetX, offsetY := (width-targetWidth)/2, (height-targetHeight)/2
	area := image.Rect(offsetX, offsetY, offsetX+targetWidth, offsetY+targetHeight)
	scale.CatmullRom.Scale(canvas, area, source, source.Bounds(), draw.Over, nil)
	dither(canvas, area)
	return nil
}

var textInkThreshold uint8 = 80

func inkMask(canvas *image.Gray, mask *image.Alpha) {
	for i, coverage := range mask.Pix {
		if coverage >= textInkThreshold {
			canvas.Pix[i] = 0
		}
	}
}

// dither applies serpentine Floyd–Steinberg error diffusion so the 1-bit panel
// can approximate grayscale tones with ordinary black/white refreshes.
func dither(canvas *image.Gray, area image.Rectangle) {
	w := area.Dx()
	current, next := make([]int, w+2), make([]int, w+2)
	for y := area.Min.Y; y < area.Max.Y; y++ {
		forward := (y-area.Min.Y)%2 == 0
		for i := 0; i < w; i++ {
			x := i
			if !forward {
				x = w - 1 - i
			}
			step := 1
			if !forward {
				step = -1
			}
			old := int(canvas.GrayAt(area.Min.X+x, y).Y) + current[x+1]/16
			value := 0
			if old >= 128 {
				value = 255
			}
			canvas.SetGray(area.Min.X+x, y, color.Gray{Y: uint8(value)})
			diff := old - value
			current[x+1+step] += diff * 7
			next[x+1-step] += diff * 3
			next[x+1] += diff * 5
			next[x+1+step] += diff
		}
		current, next = next, current
		clear(next)
	}
}

func pack(canvas *image.Gray) device.Frame {
	var frame device.Frame
	for row := 0; row < device.Height; row++ {
		for col := 0; col < device.Width; col++ {
			if canvas.GrayAt(width-1-row, col).Y >= 128 {
				offset := row*device.Width + col
				frame[offset/8] |= 0x80 >> (offset % 8)
			}
		}
	}
	return frame
}
