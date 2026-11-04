package filestore

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/dataset"
)

func addFolder(d *dataset.Data) error {
	now := time.Now().UTC()
	d.Folders = append(d.Folders, dataset.Folder{ID: catalog.NewID(), Name: "Папка", NormalizedName: "папка", CreatedAt: now, UpdatedAt: now})
	return nil
}
func TestAtomicWriteAndExternalConflict(t *testing.T) {
	p := filepath.Join(t.TempDir(), "data.yaml")
	s, err := Open(p, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.Update(ctx, addFolder); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(p)
	s.Failpoint = func(stage string) error {
		if stage == "before-replace" {
			return errors.New("disk failure")
		}
		return nil
	}
	if err = s.Update(ctx, func(d *dataset.Data) error {
		d.Folders[0].Name = "Изменено"
		d.Folders[0].NormalizedName = "изменено"
		return nil
	}); err == nil {
		t.Fatal("failed write succeeded")
	}
	doc, _ := s.Snapshot(ctx)
	after, _ := os.ReadFile(p)
	if string(after) != string(original) || doc.Data.Folders[0].Name != "Папка" {
		t.Fatal("candidate published")
	}
	s.Failpoint = nil
	if err = os.WriteFile(p, append(original, []byte("\n# external\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.Update(ctx, func(*dataset.Data) error { return nil }); err == nil {
		t.Fatal("external edit overwritten")
	}
}

func TestUncertainPublicationStopsWrites(t *testing.T) {
	p := filepath.Join(t.TempDir(), "data.yaml")
	s, err := Open(p, true)
	if err != nil {
		t.Fatal(err)
	}
	s.Failpoint = func(stage string) error {
		if stage == "after-replace" {
			return errors.New("confirmation lost")
		}
		return nil
	}
	if err = s.Update(context.Background(), addFolder); err == nil {
		t.Fatal("uncertain commit acknowledged")
	}
	select {
	case <-s.InstanceLost():
	default:
		t.Fatal("instance not faulted")
	}
	if err = s.Ping(context.Background()); err == nil {
		t.Fatal("faulty backend ready")
	}
	s.Close()
	reopened, err := Open(p, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	d, err := reopened.Snapshot(context.Background())
	if err != nil || len(d.Data.Folders) != 1 {
		t.Fatal("full new state not recoverable", err)
	}
}

func TestProcessLock(t *testing.T) {
	if p := os.Getenv("QAG_LOCK_CHILD"); p != "" {
		s, e := Open(p, true)
		if e != nil {
			os.Exit(3)
		}
		s.Close()
		os.Exit(0)
	}
	p := filepath.Join(t.TempDir(), "data.yaml")
	s, err := Open(p, true)
	if err != nil {
		t.Fatal(err)
	}
	child := func() error {
		c := exec.Command(os.Args[0], "-test.run=^TestProcessLock$")
		c.Env = append(os.Environ(), "QAG_LOCK_CHILD="+p)
		return c.Run()
	}
	if err = child(); err == nil {
		t.Fatal("second process acquired file")
	}
	s.Close()
	if err = child(); err != nil {
		t.Fatal("released lock unusable", err)
	}
}

func TestAtomicProcessCrash(t *testing.T) {
	if p := os.Getenv("QAG_CRASH_FILE"); p != "" {
		s, e := Open(p, os.Getenv("QAG_CRASH_CREATE") == "1")
		if e != nil {
			os.Exit(21)
		}
		s.Failpoint = func(stage string) error {
			if stage == os.Getenv("QAG_CRASH_STAGE") {
				os.Exit(17)
			}
			return nil
		}
		_ = s.Update(context.Background(), func(d *dataset.Data) error {
			if len(d.Folders) == 0 {
				if e := addFolder(d); e != nil {
					return e
				}
			}
			d.Folders[0].Name = "После"
			d.Folders[0].NormalizedName = "после"
			return nil
		})
		os.Exit(22)
	}
	p := filepath.Join(t.TempDir(), "data.yaml")
	s, e := Open(p, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Update(context.Background(), addFolder); e != nil {
		t.Fatal(e)
	}
	s.Close()
	for _, stage := range []string{"before-replace", "after-replace"} {
		c := exec.Command(os.Args[0], "-test.run=^TestAtomicProcessCrash$")
		c.Env = append(os.Environ(), "QAG_CRASH_FILE="+p, "QAG_CRASH_STAGE="+stage)
		e = c.Run()
		var exit *exec.ExitError
		if !errors.As(e, &exit) || exit.ExitCode() != 17 {
			t.Fatal("child did not reach crash boundary", stage, e)
		}
		reopened, e := Open(p, false)
		if e != nil {
			t.Fatal("crash left invalid data or lock", e)
		}
		d, e := reopened.Snapshot(context.Background())
		reopened.Close()
		if e != nil {
			t.Fatal(e)
		}
		want := "Папка"
		if stage == "after-replace" {
			want = "После"
		}
		if d.Data.Folders[0].Name != want {
			t.Fatal("partial publication")
		}
	}
}

func TestAtomicFirstPublicationCrash(t *testing.T) {
	for _, stage := range []string{"before-replace", "after-replace"} {
		t.Run(stage, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "new.yaml")
			c := exec.Command(os.Args[0], "-test.run=^TestAtomicProcessCrash$")
			c.Env = append(os.Environ(), "QAG_CRASH_FILE="+p, "QAG_CRASH_STAGE="+stage, "QAG_CRASH_CREATE=1")
			e := c.Run()
			var exit *exec.ExitError
			if !errors.As(e, &exit) || exit.ExitCode() != 17 {
				t.Fatal("crash boundary", e)
			}
			s, e := Open(p, true)
			if e != nil {
				t.Fatal("first publication left invalid file or alias", e)
			}
			defer s.Close()
			d, e := s.Snapshot(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			want := 0
			if stage == "after-replace" {
				want = 1
			}
			if len(d.Data.Folders) != want {
				t.Fatal("first publication changed partially")
			}
		})
	}
}
