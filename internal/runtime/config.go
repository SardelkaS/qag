package runtime

import (
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"qag/internal/demo"
	"strconv"
	"strings"
)

type Config struct {
	Address         string
	DatabaseURL     string
	SecretKey       []byte
	DevOrigin       string
	DemoDatabaseURL string
	DemoGRPCAddress string
}

func LoadConfig(lookup func(string) string) (Config, error) {
	c := Config{Address: "127.0.0.1:8080", DatabaseURL: lookup("QAG_DATABASE_URL"), DevOrigin: lookup("QAG_DEV_ORIGIN")}
	if address := lookup("QAG_ADDRESS"); address != "" {
		c.Address = address
	}
	host, portText, err := net.SplitHostPort(c.Address)
	ip := net.ParseIP(host)
	port, portErr := strconv.Atoi(portText)
	if err != nil || ip == nil || !ip.IsLoopback() || portErr != nil || port < 1 || port > 65535 {
		return Config{}, errors.New("QAG_ADDRESS должен быть loopback-адресом с портом 1–65535")
	}
	u, err := url.Parse(c.DatabaseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return Config{}, errors.New("задайте корректный QAG_DATABASE_URL для отдельной PostgreSQL-БД приложения")
	}
	c.DemoDatabaseURL = lookup("QAG_DEMO_DATABASE_URL")
	if _, _, err = demo.DatabaseConfig(c.DatabaseURL, c.DemoDatabaseURL); err != nil {
		return Config{}, err
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
	c.SecretKey, err = base64.StdEncoding.DecodeString(lookup("QAG_SECRET_KEY"))
	if err != nil || len(c.SecretKey) != 32 {
		return Config{}, errors.New("QAG_SECRET_KEY должен содержать 32 байта в base64")
	}
	if c.DevOrigin != "" {
		u, err := url.Parse(c.DevOrigin)
		if err != nil || u.Scheme != "http" || !loopbackHost(u.Hostname()) || u.User != nil || strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" {
			return Config{}, errors.New("QAG_DEV_ORIGIN должен быть локальным HTTP origin без пути")
		}
		c.DevOrigin = strings.TrimSuffix(c.DevOrigin, "/")
	}
	return c, nil
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
