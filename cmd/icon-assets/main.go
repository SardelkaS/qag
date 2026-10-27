// Command icon-assets derives deterministic application sizes from the committed
// generated master. It never calls an image generation service.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	file, err := os.Open("assets/branding/qag-master.png")
	if err != nil {
		return fmt.Errorf("master icon missing: %w", err)
	}
	master, err := png.Decode(file)
	file.Close()
	if err != nil {
		return err
	}
	if err = os.MkdirAll("web/public", 0755); err != nil {
		return err
	}
	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	encoded := [][]byte{}
	for _, size := range sizes {
		dst := image.NewNRGBA(image.Rect(0, 0, size, size))
		draw.CatmullRom.Scale(dst, dst.Bounds(), master, master.Bounds(), draw.Over, nil)
		var out bytes.Buffer
		if err = png.Encode(&out, dst); err != nil {
			return err
		}
		encoded = append(encoded, out.Bytes())
		if size == 32 || size == 256 {
			if err = os.WriteFile(filepath.Join("web/public", fmt.Sprintf("qag-%d.png", size)), out.Bytes(), 0644); err != nil {
				return err
			}
		}
	}
	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, uint16(0))
	binary.Write(&ico, binary.LittleEndian, uint16(1))
	binary.Write(&ico, binary.LittleEndian, uint16(len(sizes)))
	offset := uint32(6 + 16*len(sizes))
	for i, size := range sizes {
		ico.Write([]byte{byte(size % 256), byte(size % 256), 0, 0})
		binary.Write(&ico, binary.LittleEndian, uint16(1))
		binary.Write(&ico, binary.LittleEndian, uint16(32))
		binary.Write(&ico, binary.LittleEndian, uint32(len(encoded[i])))
		binary.Write(&ico, binary.LittleEndian, offset)
		offset += uint32(len(encoded[i]))
	}
	for _, png := range encoded {
		ico.Write(png)
	}
	if err = os.WriteFile("assets/branding/qag.ico", ico.Bytes(), 0644); err != nil {
		return err
	}
	return os.WriteFile("web/public/favicon.ico", ico.Bytes(), 0644)
}
