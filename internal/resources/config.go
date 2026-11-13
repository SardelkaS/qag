package resources

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/problem"
)

var variableName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func ValidateConfig(kind string, c Config, secrets map[string]string, sourceSecrets ...map[string]string) error {
	for _, value := range []string{c.BaseURL, c.Address, c.Database, c.Username} {
		if strings.Contains(value, "${") {
			return problem.Invalid("config", "Параметры подключения задаются явно; переменные используются в запросах")
		}
	}
	if c.UnavailableReason != "" {
		return problem.Invalid("config", "Недоступность ресурса задаётся только встроенным примером")
	}
	if c.TLS.Mode != "tls" && c.TLS.Mode != "mtls" && c.TLS.Mode != "plaintext" {
		return problem.Invalid("config.tls.mode", "Выберите TLS или явный plaintext")
	}
	switch kind {
	case "http":
		u, err := url.Parse(c.BaseURL)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
			return problem.Invalid("config.baseUrl", "Укажите базовый http(s) URL без секретов, query и fragment")
		}
		if (u.Scheme == "https") != (c.TLS.Mode != "plaintext") {
			return problem.Invalid("config.tls.mode", "Режим TLS должен соответствовать схеме URL")
		}
	case "grpc", "postgresql":
		host, port, err := net.SplitHostPort(c.Address)
		portNumber, portErr := strconv.Atoi(port)
		if err != nil || host == "" || portErr != nil || portNumber < 1 || portNumber > 65535 {
			return problem.Invalid("config.address", "Укажите host:port")
		}
		if kind == "postgresql" && (strings.TrimSpace(c.Database) == "" || strings.TrimSpace(c.Username) == "") {
			return problem.Invalid("config.database", "Укажите базу данных и пользователя")
		}
	default:
		return problem.Invalid("kind", "Поддерживаются HTTP, gRPC и PostgreSQL")
	}
	if err := catalog.ValidateRequestHeaders(c.Headers); err != nil {
		return problem.Invalid("config.headers", err.Error())
	}
	sourceValues := secrets
	if len(sourceSecrets) > 0 {
		sourceValues = sourceSecrets[0]
	}
	if err := ValidateVariableDefinitions(c, sourceValues, true); err != nil {
		return err
	}
	for _, h := range c.Headers {
		if h.Source != nil {
			if kind == "postgresql" {
				return problem.Invalid("config.headers", "SQL не использует источники заголовков")
			}
			if _, err := HeaderTLS(*h.Source, sourceValues); err != nil {
				return err
			}
		}
	}
	if len(c.ProtoFiles) > 100 {
		return problem.Invalid("config.protoFiles", "Не более 100 proto-файлов")
	}
	total := 0
	for name, value := range c.ProtoFiles {
		if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "..") || strings.ContainsAny(name, "\\:") || !strings.HasSuffix(name, ".proto") {
			return problem.Invalid("config.protoFiles", "Имена импортов должны быть относительными .proto-путями")
		}
		total += len(name) + len(value)
	}
	if total > 5<<20 {
		return problem.Invalid("config.protoFiles", "Proto-bundle должен быть не больше 5 MiB")
	}
	_, err := TLS(c, secrets)
	return err
}

func TLS(c Config, secrets map[string]string) (*tls.Config, error) {
	if strings.Contains(c.TLS.ServerName, "${") {
		return nil, problem.Invalid("config.tls.serverName", "Имя TLS-сервера задаётся явно")
	}
	for _, value := range []string{c.TLS.CA, c.TLS.Certificate, secrets["PRIVATE_KEY"]} {
		if len(value) > 1<<20 || !utf8.ValidString(value) {
			return nil, problem.Invalid("config.tls", "Сертификат или ключ должен быть UTF-8 PEM размером до 1 MiB")
		}
	}
	if c.TLS.Mode == "plaintext" {
		if c.TLS.CA != "" || c.TLS.Certificate != "" {
			return nil, problem.Invalid("config.tls", "Сертификаты требуют режима TLS")
		}
		return nil, nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if c.TLS.CA != "" && !pool.AppendCertsFromPEM([]byte(c.TLS.CA)) {
		return nil, problem.Invalid("config.tls.ca", "Не удалось прочитать CA-сертификат")
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: c.TLS.ServerName}
	key := secrets["PRIVATE_KEY"]
	if c.TLS.Mode == "mtls" || c.TLS.Certificate != "" {
		pair, err := tls.X509KeyPair([]byte(c.TLS.Certificate), []byte(key))
		if err != nil {
			return nil, problem.Invalid("config.tls.certificate", "Некорректная или несовместимая пара сертификата и ключа")
		}
		config.Certificates = []tls.Certificate{pair}
	}
	return config, nil
}

type Snapshot struct {
	EnvironmentID        string            `json:"environmentId"`
	EnvironmentName      string            `json:"environmentName"`
	EnvironmentVersionID string            `json:"environmentVersionId"`
	ResourceVersions     map[string]string `json:"resourceVersions"`
	Resources            []Resource        `json:"resources"`
}
type Resolved struct {
	Prepared       bool
	Deferred       bool
	DynamicValues  map[string]string
	planning       bool
	forbidDynamic  bool
	variablesReady bool
	Resource       Resource
	Variables      map[string]string
	Secrets        map[string]string
}

func ValidateVariables(values map[string]string, secrets map[string]string) error {
	for name := range values {
		if !variableName.MatchString(name) {
			return problem.Invalid("variables", "Имена переменных должны быть в формате TASK_ID")
		}
		if _, ok := secrets[name]; ok {
			return problem.Invalid("variables", "Переменная не может одновременно быть открытой и секретной")
		}
	}
	return nil
}

func normalizeConfig(c *Config) {
	c.Variables = NormalizeResourceVariables(c.Variables)
	if c.Headers == nil {
		c.Headers = []catalog.RequestHeader{}
	}
	if c.ProtoFiles == nil {
		c.ProtoFiles = map[string]string{}
	}
}
