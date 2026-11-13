// Package filestore implements qag repositories over one durable YAML document.
package filestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"sync"

	"github.com/SardelkaS/qag/internal/dataset"
	"github.com/SardelkaS/qag/internal/privatefile"
	"github.com/SardelkaS/qag/internal/problem"
)

type Store struct {
	mu       sync.RWMutex
	path     string
	lock     *os.File
	doc      dataset.Document
	semantic [32]byte
	hash     [32]byte
	exists   bool
	fault    bool
	closed   bool
	lost     chan struct{}
	// Failpoint is a fault injection hook, set only before using the store.
	Failpoint func(string) error
}

func Open(path string, allowMissing bool) (*Store, error) {
	p, err := privatefile.Canonical(path)
	if err != nil {
		return nil, storageError()
	}
	l, err := privatefile.Lock(p + ".lock")
	if err != nil {
		if errors.Is(err, privatefile.ErrBusy) {
			return nil, err
		}
		return nil, storageError()
	}
	s := &Store{path: p, lock: l, doc: dataset.Empty(), lost: make(chan struct{})}
	f, err := os.Open(p)
	if os.IsNotExist(err) && allowMissing {
		return s, nil
	}
	if err != nil {
		s.Close()
		return nil, storageError()
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, dataset.MaxBytes+1))
	if err != nil || len(b) > dataset.MaxBytes {
		s.Close()
		return nil, storageError()
	}
	doc, err := dataset.Decode(bytes.NewReader(b))
	if err != nil {
		s.Close()
		return nil, err
	}
	encoded, e := dataset.Encode(doc)
	if e != nil {
		s.Close()
		return nil, e
	}
	s.semantic = sha256.Sum256(encoded)
	s.doc = doc
	s.hash = sha256.Sum256(b)
	s.exists = true
	return s, nil
}

func storageError() error {
	return &problem.Error{Code: "storage_unavailable", Message: "Не удалось прочитать или устойчиво записать YAML; изменения не подтверждены"}
}
func (s *Store) Path() string { return s.path }
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.lock.Close()
	}
}
func (s *Store) InstanceLost() <-chan struct{} { return s.lost }
func (s *Store) check() error {
	if s.closed || s.fault {
		return storageError()
	}
	f, err := os.Open(s.path)
	if os.IsNotExist(err) && !s.exists {
		return nil
	}
	if err != nil {
		return storageError()
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, io.LimitReader(f, dataset.MaxBytes+1)); err != nil {
		return storageError()
	}
	if !s.exists || !bytes.Equal(h.Sum(nil), s.hash[:]) {
		return problem.Conflict("YAML изменён другим приложением; остановите qag и откройте файл заново")
	}
	return nil
}
func (s *Store) Ping(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.check()
}
func (s *Store) Snapshot(ctx context.Context) (dataset.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return dataset.Document{}, err
	}
	if err := s.check(); err != nil {
		return dataset.Document{}, err
	}
	return dataset.Clone(s.doc)
}
func (s *Store) Update(ctx context.Context, apply func(*dataset.Data) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.check(); err != nil {
		return err
	}
	candidate, err := dataset.Clone(s.doc)
	if err != nil {
		return storageError()
	}
	if err = apply(&candidate.Data); err != nil {
		return err
	}
	b, err := dataset.Encode(candidate)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if s.exists && sha256.Sum256(b) == s.semantic {
		return nil
	}
	changed, err := privatefile.Atomic(s.path, b, !s.exists, s.Failpoint)
	if err != nil {
		if changed {
			s.fault = true
			close(s.lost)
		}
		return storageError()
	}
	s.doc = candidate
	s.hash = sha256.Sum256(b)
	s.semantic = s.hash
	s.exists = true
	return nil
}

func (s *Store) Replace(ctx context.Context, doc dataset.Document) error {
	return s.Update(ctx, func(d *dataset.Data) error { *d = doc.Data; return nil })
}
