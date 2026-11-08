package main

import (
	"image"
	"image/color"
)

// coverResize center-crops img to the aspect ratio w:h and scales it to w x h by
// area averaging (each output pixel is the mean of the source pixels it covers).
func coverResize(img image.Image, w, h int) *image.RGBA {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	cw, ch := sw, sh
	if sw*h > sh*w {
		cw = sh * w / h
	} else {
		ch = sw * h / w
	}
	x0, y0 := b.Min.X+(sw-cw)/2, b.Min.Y+(sh-ch)/2
	out := image.NewRGBA(image.Rect(0, 0, w, h))

	if cw < w || ch < h {
		// smaller than the screen: nearest neighbour
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				r, g, bl, _ := img.At(x0+x*cw/w, y0+y*ch/h).RGBA()
				out.SetRGBA(x, y, color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), 255})
			}
		}
		return out
	}

	// rows of the crop as RGB bytes, one at a time (fast paths for common types)
	rowBuf := make([]uint8, cw*3)
	readRow := func(y int) []uint8 {
		switch m := img.(type) {
		case *image.YCbCr:
			for x := 0; x < cw; x++ {
				yi := m.YOffset(x0+x, y)
				ci := m.COffset(x0+x, y)
				r, g, bl := color.YCbCrToRGB(m.Y[yi], m.Cb[ci], m.Cr[ci])
				rowBuf[3*x], rowBuf[3*x+1], rowBuf[3*x+2] = r, g, bl
			}
		case *image.Gray:
			off := m.PixOffset(x0, y)
			for x := 0; x < cw; x++ {
				v := m.Pix[off+x]
				rowBuf[3*x], rowBuf[3*x+1], rowBuf[3*x+2] = v, v, v
			}
		case *image.RGBA:
			off := m.PixOffset(x0, y)
			for x := 0; x < cw; x++ {
				p := m.Pix[off+4*x:]
				rowBuf[3*x], rowBuf[3*x+1], rowBuf[3*x+2] = p[0], p[1], p[2]
			}
		default:
			for x := 0; x < cw; x++ {
				r, g, bl, _ := img.At(x0+x, y).RGBA()
				rowBuf[3*x], rowBuf[3*x+1], rowBuf[3*x+2] = uint8(r>>8), uint8(g>>8), uint8(bl>>8)
			}
		}
		return rowBuf
	}

	sum := make([]uint64, w*3)
	cnt := make([]uint64, w)
	colOf := make([]int, cw)
	for x := range colOf {
		colOf[x] = min(w-1, x*w/cw)
	}
	oy := 0
	flush := func() {
		for x := 0; x < w; x++ {
			n := max(cnt[x], 1)
			p := out.Pix[out.PixOffset(x, oy):]
			p[0], p[1], p[2], p[3] = uint8(sum[3*x]/n), uint8(sum[3*x+1]/n), uint8(sum[3*x+2]/n), 255
			sum[3*x], sum[3*x+1], sum[3*x+2], cnt[x] = 0, 0, 0, 0
		}
	}
	for y := 0; y < ch; y++ {
		ty := min(h-1, y*h/ch)
		if ty != oy {
			flush()
			oy = ty
		}
		row := readRow(y0 + y)
		for x := 0; x < cw; x++ {
			c := colOf[x]
			sum[3*c] += uint64(row[3*x])
			sum[3*c+1] += uint64(row[3*x+1])
			sum[3*c+2] += uint64(row[3*x+2])
			cnt[c]++
		}
	}
	flush()
	return out
}
