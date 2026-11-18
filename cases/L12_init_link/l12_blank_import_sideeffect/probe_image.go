package main

import (
	"bytes"
	"errors"
	"image"
)

// pngVisible asks the process-global format registry to decode the fixture.
// It succeeds only if image/png's blank-imported init registered a decoder.
func pngVisible() bool {
	_, format, err := decodeConfigFixture()
	return err == nil && format == "png"
}

func decodeFixture() (image.Image, string, error) {
	return image.Decode(bytes.NewReader(pngBytes))
}

func decodeConfigFixture() (image.Config, string, error) {
	return image.DecodeConfig(bytes.NewReader(pngBytes))
}

// jpegLooksUnregistered checks absence through the same global registry: a
// JPEG magic prefix must yield image.ErrFormat when no JPEG decoder exists.
func jpegLooksUnregistered() bool {
	_, _, err := image.Decode(bytes.NewReader([]byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F'}))
	return errors.Is(err, image.ErrFormat)
}
