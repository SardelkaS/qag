package catalog

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var pointerEscape = regexp.MustCompile(`~(?:[^01]|$)`)

func ValidateRequestHeaders(headers []RequestHeader) error {
	seen := map[string]bool{}
	for _, h := range headers {
		if !headerPattern.MatchString(h.Name) {
			return fmt.Errorf("Некорректное имя заголовка")
		}
		if h.Source == nil {
			if strings.ContainsAny(h.Value, "\r\n") {
				return fmt.Errorf("Перенос строки в заголовке")
			}
			continue
		}
		if !ValidID(h.ID) || h.Value != "" {
			return fmt.Errorf("Для источника нужен ID и отдельный режим значения")
		}
		if seen[h.ID] {
			return fmt.Errorf("Источники заголовков должны иметь уникальные ID")
		}
		seen[h.ID] = true
		if err := ValidateHeaderSource(*h.Source); err != nil {
			return err
		}
	}
	return nil
}
func ValidateHeaderSource(s HTTPHeaderSource) error {
	u, err := url.Parse(s.URL)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("Источник: нужен абсолютный HTTP(S) URL без userinfo и fragment")
	}
	if !methodPattern.MatchString(s.Method) {
		return fmt.Errorf("Источник: выберите HTTP-метод")
	}
	if s.FetchMode != "per_attempt" && s.FetchMode != "per_run" {
		return fmt.Errorf("Источник: выберите режим получения")
	}
	if s.JSONPointer != "" && !strings.HasPrefix(s.JSONPointer, "/") || pointerEscape.MatchString(s.JSONPointer) {
		return fmt.Errorf("Источник: некорректный JSON Pointer")
	}
	if strings.ContainsAny(s.Prefix, "\r\n") {
		return fmt.Errorf("Источник: перенос строки в префиксе")
	}
	if s.BodyMode != "none" && s.BodyMode != "text" && s.BodyMode != "json" {
		return fmt.Errorf("Источник: выберите формат тела")
	}
	if s.BodyMode == "json" && !json.Valid([]byte(s.Body)) {
		return fmt.Errorf("Источник: некорректный JSON тела")
	}
	if s.TLS.Mode != "tls" && s.TLS.Mode != "mtls" && s.TLS.Mode != "plaintext" {
		return fmt.Errorf("Источник: выберите TLS/mTLS/plaintext")
	}
	if (u.Scheme == "https") != (s.TLS.Mode != "plaintext") {
		return fmt.Errorf("Источник: режим TLS не соответствует URL")
	}
	if s.TLS.Mode == "mtls" && (s.TLS.Certificate == "" || !resourcePattern.MatchString(s.PrivateKeySecret)) {
		return fmt.Errorf("Источник: укажите сертификат и имя секрета ключа mTLS")
	}
	if s.TLS.Mode != "mtls" && s.TLS.Certificate != "" {
		return fmt.Errorf("Источник: клиентский сертификат требует mTLS")
	}
	return ValidateHeaders(s.Headers)
}
