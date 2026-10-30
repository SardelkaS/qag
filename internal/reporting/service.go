package reporting

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io/fs"
	"time"

	"qag/internal/catalog"
	"qag/internal/problem"
	"qag/internal/runs"
)

type Reference struct {
	RunID           string `json:"runId"`
	SnapshotID      string `json:"snapshotId"`
	Hash            string `json:"hash"`
	IncludeExpected bool   `json:"includeExpected"`
}
type File struct {
	Name, MIMEType string
	Data           []byte
}
type Service struct {
	Cache           *Cache
	Runs            *runs.Repository
	Catalog         *catalog.Repository
	Assets          fs.FS
	ClipboardNative bool
	slots           chan struct{}
}

func NewService(r *runs.Repository, c *catalog.Repository, assets fs.FS, native bool) *Service {
	return &Service{Cache: NewCache(), Runs: r, Catalog: c, Assets: assets, ClipboardNative: native, slots: make(chan struct{}, 2)}
}
func (s *Service) Prepare(ctx context.Context, id string, expected bool) (runs.Report, error) {
	r, err := s.Runs.ReportSnapshot(ctx, id, expected)
	if err != nil {
		return runs.Report{}, err
	}
	r.ClipboardNative = s.ClipboardNative
	if err = s.Cache.Put(&r); err != nil {
		return runs.Report{}, err
	}
	return r, nil
}
func (s *Service) Cached(ref Reference) (runs.Report, error) {
	return s.Cache.Get(ref.SnapshotID, ref.Hash, ref.RunID, ref.IncludeExpected)
}
func (s *Service) hydrate(ctx context.Context, r *runs.Report) error {
	for i := range r.Images {
		if err := ctx.Err(); err != nil {
			return err
		}
		image := &r.Images[i]
		meta, data, err := s.Catalog.ReadImage(ctx, r.OwnerCaseID, image.ID)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(data)
		if meta.ContentType != image.MIME || meta.Width != image.Width || meta.Height != image.Height || hex.EncodeToString(hash[:]) != image.SHA256 {
			return problem.Invalid("image", "Эталон не соответствует подготовленному срезу")
		}
		image.Data = base64.StdEncoding.EncodeToString(data)
	}
	return nil
}
func (s *Service) WithReport(ctx context.Context, ref Reference, apply func(context.Context, runs.Report) ([]File, error)) ([]File, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return nil, &problem.Error{Code: "busy", Message: "Уже подготавливаются два отчёта; повторите позже"}
	}
	r, err := s.Cached(ref)
	if err != nil {
		return nil, err
	}
	if err = s.hydrate(ctx, &r); err != nil {
		return nil, err
	}
	files, err := apply(ctx, r)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, problem.Invalid("report", "Подготовка отчёта отменена или превысила время")
	}
	return files, nil
}
func (s *Service) Export(ctx context.Context, ref Reference, format string) ([]File, error) {
	return s.WithReport(ctx, ref, func(ctx context.Context, r runs.Report) ([]File, error) { return Render(ctx, s.Assets, r, format) })
}
