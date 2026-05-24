package skin

import (
	"fmt"
	"image"
	"image/png"
	"os"
)

type ImageData struct {
	RGBA   []byte
	Width  int
	Height int
}

func LoadImage(path string) (*ImageData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open skin image: %w", err)
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode skin image: %w", err)
	}

	nrgba := ensureNRGBA(img)

	expected := nrgba.Bounds().Dx() * nrgba.Bounds().Dy() * 4
	if len(nrgba.Pix) != expected {
		return nil, fmt.Errorf("unexpected RGBA data size: got %d, expected %d", len(nrgba.Pix), expected)
	}

	return &ImageData{
		RGBA:   nrgba.Pix,
		Width:  nrgba.Bounds().Dx(),
		Height: nrgba.Bounds().Dy(),
	}, nil
}

func ensureNRGBA(img image.Image) *image.NRGBA {
	if nrgba, ok := img.(*image.NRGBA); ok {
		return nrgba
	}

	bounds := img.Bounds()
	nrgba := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			nrgba.Set(x, y, img.At(x, y))
		}
	}
	return nrgba
}
