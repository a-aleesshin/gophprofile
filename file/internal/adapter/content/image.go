package content

import (
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math"

	xdraw "golang.org/x/image/draw"

	_ "golang.org/x/image/webp"
)

const (
	fitCover  = "cover"
	fitInside = "inside"
)

type encoder struct {
	mime   string
	ext    string
	encode func(w io.Writer, img image.Image, quality int) error
}

var encoders = map[string]encoder{
	"jpeg": {mime: "image/jpeg", ext: "jpg", encode: encodeJPEG},
	"jpg":  {mime: "image/jpeg", ext: "jpg", encode: encodeJPEG},
	"png":  {mime: "image/png", ext: "png", encode: encodePNG},
}

func encodeJPEG(w io.Writer, img image.Image, quality int) error {
	if quality <= 0 || quality > 100 {
		quality = jpeg.DefaultQuality
	}

	return jpeg.Encode(w, flatten(img), &jpeg.Options{Quality: quality})
}

func encodePNG(w io.Writer, img image.Image, _ int) error {
	return png.Encode(w, img)
}

func flatten(img image.Image) image.Image {
	if opaque, ok := img.(interface{ Opaque() bool }); ok && opaque.Opaque() {
		return img
	}

	bounds := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))

	xdraw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, xdraw.Src)
	xdraw.Draw(dst, dst.Bounds(), img, bounds.Min, xdraw.Over)

	return dst
}

func applyOrientation(img image.Image, orientation int) image.Image {
	if orientation <= orientationTopLeft {
		return img
	}

	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	swapped := orientation >= orientationLeftTop

	dstWidth, dstHeight := width, height
	if swapped {
		dstWidth, dstHeight = height, width
	}

	dst := image.NewRGBA(image.Rect(0, 0, dstWidth, dstHeight))

	for y := range dstHeight {
		for x := range dstWidth {
			var sx, sy int

			switch orientation {
			case orientationTopRight:
				sx, sy = width-1-x, y
			case orientationBottomRight:
				sx, sy = width-1-x, height-1-y
			case orientationBottomLeft:
				sx, sy = x, height-1-y
			case orientationLeftTop:
				sx, sy = y, x
			case orientationRightTop:
				sx, sy = y, height-1-x
			case orientationRightBottom:
				sx, sy = width-1-y, height-1-x
			case orientationLeftBottom:
				sx, sy = width-1-y, x
			default:
				sx, sy = x, y
			}

			dst.Set(x, y, img.At(bounds.Min.X+sx, bounds.Min.Y+sy))
		}
	}

	return dst
}

func orientationSwapsSides(orientation int) bool {
	return orientation >= orientationLeftTop
}

func resize(src image.Image, width, height int, fit string) (image.Image, error) {
	bounds := src.Bounds()
	sourceWidth, sourceHeight := bounds.Dx(), bounds.Dy()

	if sourceWidth <= 0 || sourceHeight <= 0 {
		return nil, fmt.Errorf("%w: source has no pixels", ErrUnsupportedVariant)
	}

	switch fit {
	case fitCover:
		target := float64(width) / float64(height)
		source := float64(sourceWidth) / float64(sourceHeight)

		cropWidth, cropHeight := sourceWidth, sourceHeight

		if source > target {
			cropWidth = int(math.Round(float64(sourceHeight) * target))
		} else {
			cropHeight = int(math.Round(float64(sourceWidth) / target))
		}

		cropWidth = max(1, min(cropWidth, sourceWidth))
		cropHeight = max(1, min(cropHeight, sourceHeight))

		left := bounds.Min.X + (sourceWidth-cropWidth)/2
		top := bounds.Min.Y + (sourceHeight-cropHeight)/2

		dst := image.NewRGBA(image.Rect(0, 0, width, height))

		xdraw.CatmullRom.Scale(dst, dst.Bounds(), src,
			image.Rect(left, top, left+cropWidth, top+cropHeight), xdraw.Src, nil)

		return dst, nil

	case fitInside:
		scale := math.Min(float64(width)/float64(sourceWidth), float64(height)/float64(sourceHeight))
		if scale >= 1 {
			return src, nil
		}

		dstWidth := max(1, int(math.Round(float64(sourceWidth)*scale)))
		dstHeight := max(1, int(math.Round(float64(sourceHeight)*scale)))

		dst := image.NewRGBA(image.Rect(0, 0, dstWidth, dstHeight))

		xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, xdraw.Src, nil)

		return dst, nil

	default:
		return nil, fmt.Errorf("%w: unknown fit %q", ErrUnsupportedVariant, fit)
	}
}
