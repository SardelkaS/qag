package catalog

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestExpectedImageFormatsAndLimits(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	var p, j bytes.Buffer
	_ = png.Encode(&p, img)
	_ = jpeg.Encode(&j, img, nil)
	w, err := os.ReadFile("testdata/pixel.webp")
	if err != nil {
		t.Fatal(err)
	}
	for format, data := range map[string][]byte{"image/png": p.Bytes(), "image/jpeg": j.Bytes(), "image/webp": w} {
		info, decoded, err := DecodeExpectedImage(ImageUpload{Filename: "C:\\private\\test.img", DataBase64: base64.StdEncoding.EncodeToString(data)})
		if err != nil || info.ContentType != format || info.Filename != "test.img" || len(info.SHA256) != 64 || !bytes.Equal(data, decoded) {
			t.Fatalf("format %s: %v", format, err)
		}
	}
	for _, bad := range [][]byte{[]byte("<svg/>"), []byte("<html>not an image</html>"), p.Bytes()[:len(p.Bytes())/2]} {
		if _, _, err := DecodeExpectedImage(ImageUpload{Filename: "fake.png", DataBase64: base64.StdEncoding.EncodeToString(bad)}); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
	if _, _, err := DecodeExpectedImage(ImageUpload{DataBase64: strings.Repeat("A", base64.StdEncoding.EncodedLen(MaxImageBytes)+4)}); err == nil {
		t.Fatal("byte limit bypassed")
	}
	// A valid PNG header advertises a huge image; reject from its dimensions
	// before allocating/decompressing a pixel buffer.
	huge := append([]byte{}, p.Bytes()...)
	binary.BigEndian.PutUint32(huge[16:20], 100000)
	binary.BigEndian.PutUint32(huge[20:24], 100000)
	binary.BigEndian.PutUint32(huge[29:33], crc32.ChecksumIEEE(huge[12:29]))
	if _, _, err := DecodeExpectedImage(ImageUpload{DataBase64: base64.StdEncoding.EncodeToString(huge)}); err == nil {
		t.Fatal("pixel limit bypassed")
	}
}
