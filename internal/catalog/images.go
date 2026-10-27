package catalog

import "qag/internal/dbscope"

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"github.com/jackc/pgx/v5"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"qag/internal/builtin"
	"qag/internal/problem"
	"strings"
	"unicode"
)

const MaxImageBytes = 10 << 20

type ExpectedImage struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	SizeBytes   int    `json:"sizeBytes"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	SHA256      string `json:"sha256"`
}
type ImageUpload struct {
	Filename   string `json:"filename"`
	DataBase64 string `json:"dataBase64"`
}

func DecodeExpectedImage(upload ImageUpload) (ExpectedImage, []byte, error) {
	fail := func() (ExpectedImage, []byte, error) {
		return ExpectedImage{}, nil, problem.Invalid("image", "Нужен PNG, JPEG или WebP до 10 MiB и 20 миллионов пикселей")
	}
	if len(upload.DataBase64) > base64.StdEncoding.EncodedLen(MaxImageBytes) {
		return fail()
	}
	data, err := base64.StdEncoding.Strict().DecodeString(upload.DataBase64)
	if err != nil || len(data) == 0 || len(data) > MaxImageBytes {
		return fail()
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width) > 20000000/int64(cfg.Height) {
		return fail()
	}
	mime := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "webp": "image/webp"}[format]
	if mime == "" {
		return fail()
	}
	if _, _, err = image.Decode(bytes.NewReader(data)); err != nil {
		return fail()
	}
	name := strings.ReplaceAll(upload.Filename, "\\", "/")
	parts := strings.Split(name, "/")
	name = parts[len(parts)-1]
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	runes := []rune(name)
	if len(runes) > 255 {
		name = string(runes[:255])
	}
	if name == "" {
		name = "изображение"
	}
	sum := sha256.Sum256(data)
	return ExpectedImage{Filename: name, ContentType: mime, SizeBytes: len(data), Width: cfg.Width, Height: cfg.Height, SHA256: hex.EncodeToString(sum[:])}, data, nil
}
func (r *Repository) UploadImage(ctx context.Context, id string, upload ImageUpload) (ExpectedImage, error) {
	info, data, err := DecodeExpectedImage(upload)
	if err != nil {
		return info, err
	}
	err = r.transaction(ctx, func(tx pgx.Tx) error {
		c, err := getCase(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if c.ReadOnly {
			return builtin.Protect(ctx, tx, id)
		}
		if c.Archived || c.ParentArchived {
			return problem.Archived()
		}
		return problem.Database(tx.QueryRow(ctx, dbscope.Render(tx, `INSERT INTO qag.expected_images(test_case_id,filename,content_type,size_bytes,width,height,sha256,data) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`), id, info.Filename, info.ContentType, info.SizeBytes, info.Width, info.Height, info.SHA256, data).Scan(&info.ID))
	})
	return info, err
}
func (r *Repository) ReadImage(ctx context.Context, id, imageID string) (ExpectedImage, []byte, error) {
	var info ExpectedImage
	var data []byte
	if !ValidID(id) || !ValidID(imageID) {
		return info, nil, problem.NotFound()
	}
	err := r.pool.QueryRow(ctx, dbscope.Render(r.pool, `SELECT id::text,filename,content_type,size_bytes,width,height,sha256,data FROM qag.expected_images WHERE id=$1 AND test_case_id=$2`), imageID, id).Scan(&info.ID, &info.Filename, &info.ContentType, &info.SizeBytes, &info.Width, &info.Height, &info.SHA256, &data)
	return info, data, problem.Database(err)
}
func validateImageOwners(ctx context.Context, db queryer, id string, steps []Step) error {
	refs := []ImageRef{}
	for _, s := range steps {
		if s.Expected != nil {
			refs = append(refs, s.Expected.Images...)
		}
		for _, b := range s.Blocks {
			refs = append(refs, b.Expected.Images...)
		}
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref.ImageID] {
			continue
		}
		seen[ref.ImageID] = true
		var exists bool
		if err := db.QueryRow(ctx, dbscope.Render(db, `SELECT EXISTS(SELECT 1 FROM qag.expected_images WHERE id=$1 AND test_case_id=$2)`), ref.ImageID, id).Scan(&exists); err != nil {
			return problem.Database(err)
		}
		if !exists {
			return problem.Invalid("images", "Изображение отсутствует или относится к другому кейсу")
		}
	}
	return nil
}
func (r *Repository) CheckImages(ctx context.Context, id string, steps []Step) error {
	return validateImageOwners(ctx, r.pool, id, steps)
}
