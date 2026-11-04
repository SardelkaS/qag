package runtime

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/SardelkaS/qag/internal/dbscope"
	"github.com/SardelkaS/qag/internal/demo"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Storage         string
	DataFile        string
	Namespace       dbscope.Namespace
	Address         string
	DatabaseURL     string
	SecretKey       []byte
	DevOrigin       string
	DemoDatabaseURL string
	DemoGRPCAddress string
	NetworkMode     string
	PublicOrigin    string
}

// FileConfig is the private, versioned installation document shared with tools.
type FileConfig struct {
	Version          int               `json:"version"`
	Values           map[string]string `json:"values"`
	PostgresPassword string            `json:"postgresPassword"`
}

func ReadConfigFile(path string) (FileConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return FileConfig{}, errors.New("не удалось прочитать QAG_CONFIG_FILE")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > 1<<20 {
		return FileConfig{}, errors.New("некорректный формат QAG_CONFIG_FILE")
	}
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	var c FileConfig
	if d.Decode(&c) != nil || c.Version != 1 || c.Values == nil {
		return FileConfig{}, errors.New("некорректный формат QAG_CONFIG_FILE")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return FileConfig{}, errors.New("некорректный формат QAG_CONFIG_FILE")
	}
	return c, nil
}

func LoadConfig(lookup func(string) string) (Config, error) {
	return LoadConfigEnv(func(k string) (string, bool) { v := lookup(k); return v, v != "" })
}

func LoadConfigEnv(env func(string) (string, bool)) (Config, error) { return loadConfigEnv(env, false) }

func LoadDataConfigEnv(env func(string) (string, bool)) (Config, error) {
	return loadConfigEnv(env, true)
}

func loadConfigEnv(env func(string) (string, bool), dataOnly bool) (Config, error) {
	values := map[string]string{}
	if path, _ := env("QAG_CONFIG_FILE"); path != "" {
		f, err := ReadConfigFile(path)
		if err != nil {
			return Config{}, err
		}
		values = f.Values
		if values["QAG_STORAGE"] == "" && values["QAG_DATABASE_URL"] == "" && values["QAG_DATA_FILE"] == "" {
			return Config{}, errors.New("QAG_CONFIG_FILE не содержит настройки хранилища")
		}
	}
	lookup := func(k string) string {
		if v, ok := env(k); ok {
			return v
		}
		return values[k]
	}
	if lookup("QAG_STORAGE") == "" {
		_, explicitDB := env("QAG_DATABASE_URL")
		if explicitDB && lookup("QAG_DATABASE_URL") == "" && lookup("QAG_DATA_FILE") == "" {
			return Config{}, errors.New("QAG_DATABASE_URL задан пустым")
		}
	}
	return loadValues(lookup, dataOnly)
}

func loadValues(lookup func(string) string, dataOnly bool) (Config, error) {
	c := Config{Address: "127.0.0.1:8080", DatabaseURL: lookup("QAG_DATABASE_URL"), DevOrigin: lookup("QAG_DEV_ORIGIN")}
	c.Storage = lookup("QAG_STORAGE")
	c.DataFile = lookup("QAG_DATA_FILE")
	if c.Storage == "" {
		if c.DatabaseURL != "" && c.DataFile != "" {
			return Config{}, errors.New("одновременно заданы БД и файл; укажите QAG_STORAGE")
		}
		if c.DatabaseURL != "" {
			c.Storage = "postgres"
		} else {
			c.Storage = "yaml"
		}
	}
	if c.Storage != "postgres" && c.Storage != "yaml" {
		return Config{}, errors.New("QAG_STORAGE: допустимы postgres и yaml")
	}
	if c.Storage == "yaml" && c.DataFile == "" {
		c.DataFile = "./qag.yaml"
	}
	c.NetworkMode = lookup("QAG_NETWORK_MODE")
	c.PublicOrigin = lookup("QAG_PUBLIC_ORIGIN")
	if c.NetworkMode != "" && c.NetworkMode != "native" && c.NetworkMode != "container" {
		return Config{}, errors.New("QAG_NETWORK_MODE: допустимы native и container")
	}
	if c.NetworkMode == "container" && c.PublicOrigin == "" {
		return Config{}, errors.New("контейнерный режим требует QAG_PUBLIC_ORIGIN")
	}
	if c.PublicOrigin != "" {
		if !validLocalOrigin(c.PublicOrigin) {
			return Config{}, errors.New("QAG_PUBLIC_ORIGIN должен быть локальным HTTP origin без пути")
		}
		c.PublicOrigin = strings.TrimSuffix(c.PublicOrigin, "/")
	}
	var scopeErr error
	c.Namespace, scopeErr = dbscope.New(lookup("QAG_SCHEMA"), lookup("QAG_DEMO_SCHEMA"), lookup("QAG_TEST_MODE") == "1")
	if scopeErr != nil {
		return Config{}, scopeErr
	}
	if address := lookup("QAG_ADDRESS"); address != "" {
		c.Address = address
	}
	host, portText, err := net.SplitHostPort(c.Address)
	ip := net.ParseIP(host)
	port, portErr := strconv.Atoi(portText)
	if err != nil || ip == nil || (!ip.IsLoopback() && !(c.NetworkMode == "container" && ip.IsUnspecified())) || portErr != nil || port < 1 || port > 65535 {
		return Config{}, errors.New("QAG_ADDRESS должен быть loopback-адресом с портом 1–65535")
	}
	if c.Storage == "postgres" {
		u, err := url.Parse(c.DatabaseURL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
			return Config{}, errors.New("задайте корректный QAG_DATABASE_URL для PostgreSQL приложения")
		}
		c.DemoDatabaseURL = lookup("QAG_DEMO_DATABASE_URL")
		if !dataOnly {
			if _, _, err = demo.DatabaseConfig(c.DatabaseURL, c.DemoDatabaseURL); err != nil {
				return Config{}, err
			}
		}

	}
	c.DemoGRPCAddress = lookup("QAG_DEMO_GRPC_ADDRESS")
	if c.DemoGRPCAddress == "" {
		c.DemoGRPCAddress = "127.0.0.1:9091"
	}
	demoHost, demoPortText, demoErr := net.SplitHostPort(c.DemoGRPCAddress)
	demoIP := net.ParseIP(demoHost)
	demoPort, demoPortErr := strconv.Atoi(demoPortText)
	if demoErr != nil || demoIP == nil || !demoIP.IsLoopback() || demoPortErr != nil || demoPort < 0 || demoPort > 65535 {
		return Config{}, errors.New("QAG_DEMO_GRPC_ADDRESS должен быть loopback-адресом с портом 0–65535")
	}
	if c.Storage == "postgres" {
		c.SecretKey, err = base64.StdEncoding.DecodeString(lookup("QAG_SECRET_KEY"))
		if err != nil || len(c.SecretKey) != 32 {
			return Config{}, errors.New("QAG_SECRET_KEY должен содержать 32 байта в base64")
		}

	}
	if c.DevOrigin != "" {
		if !validLocalOrigin(c.DevOrigin) {
			return Config{}, errors.New("QAG_DEV_ORIGIN должен быть локальным HTTP origin без пути")
		}
		c.DevOrigin = strings.TrimSuffix(c.DevOrigin, "/")
	}
	return c, nil
}

func validLocalOrigin(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "http" || !loopbackHost(u.Hostname()) || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	if u.Port() == "" {
		return !strings.HasSuffix(u.Host, ":")
	}
	p, err := strconv.Atoi(u.Port())
	return err == nil && p > 0 && p <= 65535
}

// ClientAddress turns a wildcard bind address into a reachable self address.
func ClientAddress(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err == nil {
		if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
			return net.JoinHostPort("127.0.0.1", port)
		}
	}
	return address
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
