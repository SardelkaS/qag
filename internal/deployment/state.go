// Package deployment prepares private installation state without shell secrets.
package deployment

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	appruntime "qag/internal/runtime"
	"qag/internal/testfixture"
)

type Paths struct{ Config, PKI, Postgres, Data string }

var pkiNames = []string{"ca.crt", "ca.key", "server.crt", "server.key", "pg_hba.conf"}

func randomHex() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func exists(path string) bool { _, err := os.Stat(path); return err == nil }
func empty(path string) bool {
	files, err := os.ReadDir(path)
	return os.IsNotExist(err) || err == nil && len(files) == 0
}
func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	return os.Chmod(path, 0700)
}
func write(path string, data []byte) error {
	f, err := os.OpenFile(path+".tmp", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(path+".tmp", path)
}
func configPath(p Paths) string { return filepath.Join(p.Config, "installation.json") }

func validate(c appruntime.FileConfig) error {
	if c.Version != 1 || c.PostgresPassword == "" {
		return errors.New("неполная конфигурация установки")
	}
	if _, err := appruntime.LoadConfig(func(k string) string { return c.Values[k] }); err != nil {
		return err
	}
	for _, k := range []string{"QAG_DATABASE_URL", "QAG_DEMO_DATABASE_URL"} {
		u, err := url.Parse(c.Values[k])
		if err != nil || u.Host != "postgres:5432" || u.Path != "/qag" || u.User == nil {
			return errors.New("неподдерживаемое подключение установки")
		}
		pw, ok := u.User.Password()
		if !ok || pw == "" {
			return errors.New("пароль подключения отсутствует")
		}
		if k == "QAG_DATABASE_URL" && (u.User.Username() != "qag" || pw != c.PostgresPassword) {
			return errors.New("пароль PostgreSQL не согласован с подключением")
		}
		// An import must never silently weaken an existing TLS connection.
		if u.Query().Get("sslmode") != "disable" || len(u.Query()) != 1 {
			return errors.New("импорт TLS/дополнительных DSN-параметров требует явной миграции; TLS автоматически не понижается")
		}
	}
	return nil
}

// Init never replaces an incomplete installation or generates keys for old data.
func Init(p Paths) error {
	if exists(configPath(p)) {
		return Check(p)
	}
	if !empty(p.Config) || !empty(p.PKI) || !empty(p.Postgres) {
		return errors.New("неполное состояние: восстановите исходную конфигурацию, автоматическая замена запрещена")
	}
	if !empty(p.Data) {
		return errors.New("PostgreSQL уже содержит данные: сначала import-local или восстановление конфигурации")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	pass := randomHex()
	demoPass := randomHex()
	dsn := func(user, password string) string {
		return (&url.URL{Scheme: "postgresql", Host: "postgres:5432", Path: "/qag", User: url.UserPassword(user, password), RawQuery: "sslmode=disable"}).String()
	}
	c := appruntime.FileConfig{Version: 1, PostgresPassword: pass, Values: map[string]string{
		"QAG_DATABASE_URL": dsn("qag", pass), "QAG_DEMO_DATABASE_URL": dsn("qag_demo_"+randomHex()[:24], demoPass),
		"QAG_SECRET_KEY": base64.StdEncoding.EncodeToString(key), "QAG_NETWORK_MODE": "container", "QAG_ADDRESS": "0.0.0.0:8080", "QAG_PUBLIC_ORIGIN": "http://127.0.0.1:8080",
	}}
	return install(p, c, "")
}

// ImportLocal validates all input before touching target state; source is read-only.
func ImportLocal(p Paths, source string) error {
	raw, err := os.ReadFile(filepath.Join(source, "dev.env"))
	if err != nil {
		return errors.New("не удалось прочитать исходный dev.env")
	}
	env := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || env[k] != "" {
			return errors.New("некорректный или повторный параметр dev.env")
		}
		env[k] = v
	}
	c := appruntime.FileConfig{Version: 1, PostgresPassword: env["QAG_POSTGRES_PASSWORD"], Values: map[string]string{
		"QAG_SECRET_KEY": env["QAG_SECRET_KEY"], "QAG_NETWORK_MODE": "container", "QAG_ADDRESS": "0.0.0.0:8080", "QAG_PUBLIC_ORIGIN": "http://127.0.0.1:8080",
	}}
	for _, k := range []string{"QAG_DATABASE_URL", "QAG_DEMO_DATABASE_URL"} {
		u, e := url.Parse(env[k])
		if e != nil || u.User == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Path != "/qag" {
			return errors.New("неподдерживаемый локальный PostgreSQL URI")
		}
		ip := net.ParseIP(u.Hostname())
		if (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) || u.Port() != "15432" {
			return errors.New("import-local поддерживает только локальный PostgreSQL на порту 15432")
		}
		u.Host = "postgres:5432"
		c.Values[k] = u.String()
	}
	return importConfig(p, c, filepath.Join(source, "pki"))
}

func Restore(p Paths, source string) error {
	c, err := appruntime.ReadConfigFile(filepath.Join(source, "installation.json"))
	if err != nil {
		return err
	}
	return importConfig(p, c, filepath.Join(source, "pki"))
}
func importConfig(p Paths, c appruntime.FileConfig, sourcePKI string) error {
	if err := validate(c); err != nil {
		return err
	}
	// Validate a private scratch copy: EnsureLocalPKI also writes HBA.
	scratch, err := os.MkdirTemp("", "qag-pki-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	if err = copyPKI(sourcePKI, scratch); err != nil {
		return err
	}
	if err = testfixture.EnsureLocalPKI(scratch); err != nil {
		return err
	}
	if exists(configPath(p)) {
		current, e := appruntime.ReadConfigFile(configPath(p))
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(current, c) {
			return errors.New("импорт конфликтует с сохранённой конфигурацией; запись запрещена")
		}
		for _, name := range pkiNames[:4] {
			a, _ := os.ReadFile(filepath.Join(p.PKI, name))
			b, _ := os.ReadFile(filepath.Join(scratch, name))
			if !bytes.Equal(a, b) {
				return errors.New("импорт конфликтует с сохранённой PKI")
			}
		}
		return Check(p)
	}
	if !empty(p.Config) || !empty(p.PKI) || !empty(p.Postgres) {
		return errors.New("частичное состояние: восстановите исходную копию")
	}
	return install(p, c, scratch)
}
func copyPKI(from, to string) error {
	if err := privateDir(to); err != nil {
		return err
	}
	for _, name := range pkiNames[:4] {
		b, err := os.ReadFile(filepath.Join(from, name))
		if err != nil {
			return errors.New("неполная исходная PKI")
		}
		if err = os.WriteFile(filepath.Join(to, name), b, 0600); err != nil {
			return err
		}
	}
	return nil
}
func install(p Paths, c appruntime.FileConfig, sourcePKI string) error {
	if err := validate(c); err != nil {
		return err
	}
	for _, dir := range []string{p.Config, p.PKI, p.Postgres} {
		if err := privateDir(dir); err != nil {
			return err
		}
	}
	// The primary document is committed first. A crash leaves a detectable state,
	// never a reason to generate another master key.
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err = write(configPath(p), b); err != nil {
		return err
	}
	if sourcePKI != "" {
		if err = copyPKI(sourcePKI, p.PKI); err != nil {
			return err
		}
	}
	if err = testfixture.EnsureLocalPKI(p.PKI); err != nil {
		return err
	}
	for _, name := range []string{"ca.crt", "server.crt", "server.key", "pg_hba.conf"} {
		b, e := os.ReadFile(filepath.Join(p.PKI, name))
		if e != nil {
			return e
		}
		if e = write(filepath.Join(p.Postgres, name), b); e != nil {
			return e
		}
	}
	if err = write(filepath.Join(p.Postgres, "password"), []byte(c.PostgresPassword)); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err = os.Chown(p.Config, 10001, 10001); err != nil {
			return err
		}
		if err = os.Chown(configPath(p), 10001, 10001); err != nil {
			return err
		}
	}
	return write(filepath.Join(p.Config, "complete"), []byte("1\n"))
}

func Check(p Paths) error {
	c, err := appruntime.ReadConfigFile(configPath(p))
	if err != nil {
		return err
	}
	if err = validate(c); err != nil {
		return err
	}
	if !exists(filepath.Join(p.Config, "complete")) {
		return errors.New("подготовка не завершена: восстановите конфигурацию из исходной копии")
	}
	// Do not repair/regenerate missing PKI during a repeated init.
	for _, name := range pkiNames {
		if !exists(filepath.Join(p.PKI, name)) {
			return errors.New("сохранённая PKI неполна")
		}
	}
	hba, err := os.ReadFile(filepath.Join(p.PKI, "pg_hba.conf"))
	if err != nil || string(hba) != testfixture.LocalHBA {
		return errors.New("сохранённая HBA повреждена")
	}
	scratch, err := os.MkdirTemp("", "qag-pki-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	if err = copyPKI(p.PKI, scratch); err != nil {
		return err
	}
	if err = testfixture.EnsureLocalPKI(scratch); err != nil {
		return err
	}
	for _, name := range []string{"ca.crt", "server.crt", "server.key", "pg_hba.conf"} {
		a, e := os.ReadFile(filepath.Join(p.PKI, name))
		if e != nil {
			return e
		}
		b, e := os.ReadFile(filepath.Join(p.Postgres, name))
		if e != nil || !bytes.Equal(a, b) {
			return errors.New("конфигурация PostgreSQL повреждена; восстановите исходные файлы")
		}
	}
	b, err := os.ReadFile(filepath.Join(p.Postgres, "password"))
	if err != nil || string(b) != c.PostgresPassword {
		return errors.New("пароль PostgreSQL не согласован с конфигурацией")
	}
	return nil
}

// Export writes closed files to a NEW directory, suitable for docker cp. It never
// prints secret values. Installation JSON is exact; dev.env uses host addresses.
func Export(p Paths, destination string) error {
	if err := Check(p); err != nil {
		return err
	}
	if exists(destination) {
		return errors.New("каталог экспорта уже существует; перезапись запрещена")
	}
	c, err := appruntime.ReadConfigFile(configPath(p))
	if err != nil {
		return err
	}
	if err = privateDir(destination); err != nil {
		return err
	}
	b, _ := os.ReadFile(configPath(p))
	if err = write(filepath.Join(destination, "installation.json"), b); err != nil {
		return err
	}
	if err = copyPKI(p.PKI, filepath.Join(destination, "pki")); err != nil {
		return err
	}
	lines := []string{"QAG_POSTGRES_PASSWORD=" + c.PostgresPassword, "QAG_SECRET_KEY=" + c.Values["QAG_SECRET_KEY"]}
	for _, k := range []string{"QAG_DATABASE_URL", "QAG_DEMO_DATABASE_URL"} {
		u, _ := url.Parse(c.Values[k])
		u.Host = "127.0.0.1:15432"
		lines = append(lines, k+"="+u.String())
		if k == "QAG_DATABASE_URL" {
			lines = append(lines, "QAG_TEST_DATABASE_URL="+u.String())
		}
	}
	if err = write(filepath.Join(destination, "dev.env"), []byte(strings.Join(lines, "\n")+"\n")); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destination, "pki", "pg_hba.conf"), []byte(testfixture.LocalHBA), 0600)
}
