package catalog

import (
	"encoding/json"
	"fmt"
	"mime"
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
	if strings.ContainsAny(s.Prefix+s.Suffix, "\r\n") {
		return fmt.Errorf("Источник: перенос строки в тексте до или после значения")
	}
	if s.BodyMode != "none" && s.BodyMode != "text" && s.BodyMode != "json" && s.BodyMode != "form" {
		return fmt.Errorf("Источник: выберите формат тела")
	}
	if s.BodyMode == "form" {
		if s.Body != "" {
			return fmt.Errorf("Источник: форма не может содержать текстовое тело")
		}
		if _, err := EncodeSourceForm(s.Form); err != nil {
			return err
		}
		if err := ValidateFormHeaders(s.Headers, true); err != nil {
			return err
		}
	} else if len(s.Form) != 0 {
		return fmt.Errorf("Источник: поля формы допустимы только в формате form-urlencoded")
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

// EncodeSourceForm preserves repeated names and authored order. Inputs are raw
// values; callers expand allowed references before encoding exactly once.
func EncodeSourceForm(form []KeyValue) (string, error) {
	parts := make([]string, 0, len(form))
	for _, pair := range form {
		if strings.TrimSpace(pair.Name) == "" {
			if pair.Value == "" {
				continue
			}
			return "", fmt.Errorf("Источник: у поля формы с заполненным значением должно быть имя")
		}
		parts = append(parts, url.QueryEscape(pair.Name)+"="+url.QueryEscape(pair.Value))
	}
	return strings.Join(parts, "&"), nil
}

func ValidateFormHeaders(headers []KeyValue, allowTemplates bool) error {
	count := 0
	for _, h := range headers {
		if !strings.EqualFold(h.Name, "Content-Type") {
			continue
		}
		count++
		if count > 1 {
			return fmt.Errorf("Источник: Content-Type формы должен быть задан один раз")
		}
		if allowTemplates && strings.Contains(h.Value, "${") {
			continue
		}
		mediaType, params, err := mime.ParseMediaType(h.Value)
		if err != nil || mediaType != "application/x-www-form-urlencoded" {
			return fmt.Errorf("Источник: для формы нужен Content-Type application/x-www-form-urlencoded")
		}
		for key, value := range params {
			if key != "charset" || !strings.EqualFold(value, "utf-8") {
				return fmt.Errorf("Источник: форма поддерживает только кодировку UTF-8")
			}
		}
	}
	return nil
}
