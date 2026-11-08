package main

import (
	"encoding/binary"
	"image"
)

// exifOrientation reads the EXIF orientation (1..8) of JPEG data, 1 if none.
// Phone photos are often stored sideways with this tag saying how to turn them;
// the reMarkable app embeds such JPEGs unchanged and sizes the page upright.
func exifOrientation(jpg []byte) int {
	if len(jpg) < 4 || jpg[0] != 0xFF || jpg[1] != 0xD8 {
		return 1
	}
	for p := 2; p+4 <= len(jpg); {
		if jpg[p] != 0xFF {
			return 1
		}
		marker := jpg[p+1]
		if marker == 0xD8 || (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 {
			p += 2
			continue
		}
		if marker == 0xDA || marker == 0xD9 { // image data starts: no EXIF before it
			return 1
		}
		n := int(binary.BigEndian.Uint16(jpg[p+2:]))
		seg := jpg[p+4 : min(len(jpg), p+2+n)]
		if marker == 0xE1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		p += 2 + n
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	ifd := int(bo.Uint32(t[4:]))
	if ifd+2 > len(t) {
		return 1
	}
	count := int(bo.Uint16(t[ifd:]))
	for i := 0; i < count; i++ {
		e := ifd + 2 + 12*i
		if e+12 > len(t) {
			break
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			if o := int(bo.Uint16(t[e+8:])); o >= 1 && o <= 8 {
				return o
			}
		}
	}
	return 1
}

// orientSwaps: orientations 5..8 turn the picture by 90 degrees.
func orientSwaps(o int) bool { return o >= 5 && o <= 8 }

// applyOrientation turns/flips src (stored orientation) upright.
func applyOrientation(src *image.RGBA, o int) *image.RGBA {
	if o <= 1 || o > 8 {
		return src
	}
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	W, H := sw, sh
	if orientSwaps(o) {
		W, H = sh, sw
	}
	dst := image.NewRGBA(image.Rect(0, 0, W, H))
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = W-1-x, y
			case 3:
				sx, sy = W-1-x, H-1-y
			case 4:
				sx, sy = x, H-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, W-1-x
			case 7:
				sx, sy = H-1-y, W-1-x
			case 8:
				sx, sy = H-1-y, x
			}
			copy(dst.Pix[dst.PixOffset(x, y):dst.PixOffset(x, y)+4], src.Pix[src.PixOffset(sx, sy):src.PixOffset(sx, sy)+4])
		}
	}
	return dst
}
