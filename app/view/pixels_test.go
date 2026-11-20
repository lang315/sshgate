package view

import (
	"image"
	"math"

	"github.com/egoist/mygo/ui"
)

// region copies the pixels inside r, clipped to the image (1 DIP = 1 px).
// pixelRect snaps r to whole pixels by rounding, as the renderer does; a
// truncated fractional edge can land a pixel off, and font metrics decide the fraction.
func pixelRect(r ui.Rect) image.Rectangle {
	rd := func(v float32) int { return int(math.Round(float64(v))) }
	return image.Rect(rd(r.X), rd(r.Y), rd(r.X+r.W), rd(r.Y+r.H))
}

func region(img *image.RGBA, r ui.Rect) []byte {
	b := pixelRect(r).Intersect(img.Bounds())
	var out []byte
	for y := b.Min.Y; y < b.Max.Y; y++ {
		o := img.PixOffset(b.Min.X, y)
		out = append(out, img.Pix[o:o+4*b.Dx()]...)
	}
	return out
}

func sameRegion(a, b *image.RGBA, r ui.Rect) bool {
	return string(region(a, r)) == string(region(b, r))
}

// countNear counts the pixels of r whose R, G and B are each within tol of col.
func countNear(img *image.RGBA, r ui.Rect, col ui.Color, tol uint8) int {
	b := pixelRect(r).Intersect(img.Bounds())
	near := func(got, want uint8) bool {
		if got < want {
			got, want = want, got
		}
		return got-want <= tol
	}
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			o := img.PixOffset(x, y)
			if near(img.Pix[o], col.R) && near(img.Pix[o+1], col.G) && near(img.Pix[o+2], col.B) {
				n++
			}
		}
	}
	return n
}

func grow(r ui.Rect, d float32) ui.Rect {
	return ui.Rect{X: r.X - d, Y: r.Y - d, W: r.W + 2*d, H: r.H + 2*d}
}

// shot is a copy of the last frame, which a later frame may overwrite.
func shot(tt *tester) *image.RGBA {
	img := tt.Image()
	out := image.NewRGBA(img.Bounds())
	copy(out.Pix, img.Pix)
	return out
}
