// Command logo-dark derives the dark-background variant of the Vantigo logo.
//
// The logo is a navy-to-mint "V". Its navy end almost vanishes on the dark
// theme's surface (#1c1f32), so this lightens the dark tones and leaves the
// mint as it is. The variant is committed next to the original; regenerate it
// after a change to logo.png:
//
//	go run packages/frontend-shell/scripts/logo-dark.go
//
// It needs only the standard library, so it is a single file with no module.
// Run from the repository root, or pass the input and output paths:
//
//	go run packages/frontend-shell/scripts/logo-dark.go in.png out.png
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const (
	defaultIn  = "packages/frontend-shell/src/assets/logo.png"
	defaultOut = "packages/frontend-shell/src/assets/logo-dark.png"

	// Tones darker than liftFrom are raised towards liftTo in HSL lightness;
	// the weight eases in between liftFrom and liftStart, so the mint (about
	// 0.54) is left alone and the navy-to-mint gradient stays continuous.
	liftStart = 0.55
	liftFrom  = 0.30
	liftTo    = 0.64
)

func main() {
	in, out := defaultIn, defaultOut
	if len(os.Args) == 3 {
		in, out = os.Args[1], os.Args[2]
	} else if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: logo-dark [in.png out.png]")
		os.Exit(2)
	}
	if err := run(in, out); err != nil {
		fmt.Fprintln(os.Stderr, "logo-dark:", err)
		os.Exit(1)
	}
}

func run(in, out string) error {
	f, err := os.Open(in)
	if err != nil {
		return err
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		return err
	}
	b := src.Bounds()
	dst := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
			if c.A == 0 {
				continue
			}
			h, s, l := rgbToHSL(float64(c.R)/255, float64(c.G)/255, float64(c.B)/255)
			l += lift(l) * (liftTo - l)
			r, g, bl := hslToRGB(h, s, l)
			dst.SetNRGBA(x, y, color.NRGBA{R: to8(r), G: to8(g), B: to8(bl), A: c.A})
		}
	}
	o, err := os.Create(out)
	if err != nil {
		return err
	}
	if err := png.Encode(o, dst); err != nil {
		o.Close()
		return err
	}
	return o.Close()
}

// lift is how much of the way to liftTo a tone of lightness l travels: all of
// it at liftFrom and below, none at liftStart and above, smoothly between.
func lift(l float64) float64 {
	t := math.Min(1, math.Max(0, (liftStart-l)/(liftStart-liftFrom)))
	return t * t * (3 - 2*t)
}

func to8(v float64) uint8 { return uint8(math.Round(math.Min(1, math.Max(0, v)) * 255)) }

func rgbToHSL(r, g, b float64) (h, s, l float64) {
	maxC, minC := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l = (maxC + minC) / 2
	d := maxC - minC
	if d == 0 {
		return 0, 0, l
	}
	s = d / (1 - math.Abs(2*l-1))
	switch maxC {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h, s, l
}

func hslToRGB(h, s, l float64) (r, g, b float64) {
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return r + m, g + m, b + m
}
