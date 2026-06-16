//go:build ignore

// Generates Icon.png — a simple placeholder launcher icon (rising signal bars
// on a dark background), using only the standard library so there's no extra
// dependency. Run from this directory: go run gen.go
package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
)

func main() {
	const s = 512
	bg := color.RGBA{0x0d, 0x11, 0x17, 0xff} // near-black
	fg := color.RGBA{0x3d, 0xd6, 0x8c, 0xff} // signal green

	img := image.NewRGBA(image.Rect(0, 0, s, s))
	for y := 0; y < s; y++ {
		for x := 0; x < s; x++ {
			img.Set(x, y, bg)
		}
	}

	// Four rising bars centred in the canvas.
	const bw, gap, base = 72, 32, 96
	x0 := (s - (4*bw + 3*gap)) / 2
	for i := 0; i < 4; i++ {
		bx := x0 + i*(bw+gap)
		bh := (i + 1) * 80
		for y := s - base - bh; y < s-base; y++ {
			for x := bx; x < bx+bw; x++ {
				img.Set(x, y, fg)
			}
		}
	}

	f, err := os.Create("Icon.png")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}
