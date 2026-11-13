// Command module-fixture prepares an isolated file Go proxy for installation
// verification before a revision is published. It never reads local app data.
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const modulePath = "github.com/SardelkaS/qag"
const version = "v0.0.0-install-fixture"

func main() {
	output := flag.String("output", ".local/install-proxy", "new proxy directory")
	flag.Parse()
	if err := run(*output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}
func write(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}
func run(output string) error {
	abs, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if _, err = os.Stat(abs); !os.IsNotExist(err) {
		return fmt.Errorf("выберите новый каталог proxy")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	mod, err := os.ReadFile("go.mod")
	if err != nil || !bytes.Contains(mod, []byte("module "+modulePath+"\n")) {
		return fmt.Errorf("запускайте из корня qag")
	}
	cacheOut, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		return err
	}
	cache := strings.TrimSpace(string(cacheOut))
	list, err := exec.Command("go", "list", "-m", "-json", "all").Output()
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(list))
	for {
		var m struct {
			Path, Version string
			Main          bool
		}
		if e := d.Decode(&m); e == io.EOF {
			break
		} else if e != nil {
			return e
		}
		if m.Main {
			continue
		}
		base := filepath.Join(escape(m.Path), "@v", escape(m.Version))
		for _, ext := range []string{".mod", ".info", ".zip"} {
			b, e := os.ReadFile(filepath.Join(cache, "cache", "download", base+ext))
			if e != nil {
				return fmt.Errorf("нужен кеш Go-модуля %s %s (%s)", m.Path, m.Version, ext)
			}
			if e = write(filepath.Join(abs, base+ext), b); e != nil {
				return e
			}
		}
	}
	base := filepath.Join(abs, escape(modulePath), "@v")
	if err = os.MkdirAll(base, 0700); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(base, version+".zip"))
	if err != nil {
		return err
	}
	z := zip.NewWriter(f)
	add := func(name string) error {
		b, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if e != nil {
			return e
		}
		h := &zip.FileHeader{Name: modulePath + "@" + version + "/" + name, Method: zip.Deflate, Modified: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}
		h.SetMode(0644)
		w, e := z.CreateHeader(h)
		if e != nil {
			return e
		}
		_, e = w.Write(b)
		return e
	}
	for _, name := range []string{"go.mod", "go.sum", "LICENSE"} {
		if err = add(name); err != nil {
			return err
		}
	}
	for _, dir := range []string{"cmd", "internal", "web"} {
		err = filepath.WalkDir(dir, func(p string, e fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			name := filepath.ToSlash(p)
			if e.IsDir() {
				if dir == "web" && name != "web" && name != "web/bundle" && !strings.HasPrefix(name, "web/bundle/") {
					return filepath.SkipDir
				}
				return nil
			}
			if e.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("module source must not contain links")
			}
			ext := filepath.Ext(name)
			include := name == "internal/mcpserver/guide.md" || ext == ".go" || ext == ".proto" || ext == ".sql" || name == "cmd/qag/icon_windows_amd64.syso" || name == "cmd/qag/icon_windows_arm64.syso" || strings.HasPrefix(name, "web/bundle/")
			if include {
				return add(name)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if err = z.Close(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = write(filepath.Join(base, version+".mod"), mod); err != nil {
		return err
	}
	info, _ := json.Marshal(map[string]string{"Version": version, "Time": "2026-11-04T00:00:00Z"})
	if err = write(filepath.Join(base, version+".info"), info); err != nil {
		return err
	}
	if err = write(filepath.Join(base, "list"), []byte(version+"\n")); err != nil {
		return err
	}
	p := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" {
		p = "/" + p
	}
	proxy := (&url.URL{Scheme: "file", Path: p}).String()
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"proxy": proxy, "version": version, "module": modulePath})
}
