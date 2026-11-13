package resources

import (
	"crypto/tls"
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/problem"
	"net/url"
)

func HeaderTLS(source catalog.HTTPHeaderSource, secrets map[string]string) (*tls.Config, error) {
	keys := map[string]string{}
	if source.TLS.Mode == "mtls" {
		key, ok := secrets[source.PrivateKeySecret]
		if !ok || key == "" {
			return nil, problem.Invalid("source.privateKeySecret", "Источник: секрет клиентского ключа не задан")
		}
		keys["PRIVATE_KEY"] = key
	}
	return TLS(Config{TLS: source.TLS}, keys)
}
func (res Resolved) prepareRequestHeaders(headers []catalog.RequestHeader) ([]catalog.RequestHeader, error) {
	for i := range headers {
		h := &headers[i]
		if h.Source == nil {
			value, err := res.Expand(h.Value, true, nil)
			if err != nil {
				return nil, err
			}
			h.Value = value
			continue
		}
		s := h.Source
		if err := catalog.ValidateHeaderSource(*s); err != nil {
			return nil, problem.Invalid("source", err.Error())
		}
		u, err := url.Parse(s.URL)
		if err != nil {
			return nil, problem.Invalid("source.url", "Источник: некорректный URL")
		}
		raw, err := res.Expand(u.Path, false, url.PathEscape)
		if err != nil {
			return nil, err
		}
		u.Path, err = url.PathUnescape(raw)
		if err != nil {
			return nil, problem.Invalid("source.url", "Источник: некорректный путь")
		}
		u.RawPath = raw
		q := u.Query()
		for name, values := range q {
			for j, value := range values {
				values[j], err = res.Expand(value, false, nil)
				if err != nil {
					return nil, err
				}
			}
			q[name] = values
		}
		u.RawQuery = q.Encode()
		s.URL = u.String()
		for j := range s.Query {
			s.Query[j].Value, err = res.Expand(s.Query[j].Value, false, nil)
			if err != nil {
				return nil, err
			}
		}
		for j := range s.Headers {
			s.Headers[j].Value, err = res.Expand(s.Headers[j].Value, true, nil)
			if err != nil {
				return nil, err
			}
		}
		if s.BodyMode == "json" {
			s.Body, err = res.expandJSONAllowed(s.Body, true)
		} else if s.BodyMode == "form" {
			form := make([]catalog.KeyValue, 0, len(s.Form))
			for _, pair := range s.Form {
				if pair.Name == "" && pair.Value == "" {
					continue
				}
				pair.Name, err = res.Expand(pair.Name, false, nil)
				if err != nil {
					return nil, err
				}
				pair.Value, err = res.Expand(pair.Value, true, nil)
				if err != nil {
					return nil, err
				}
				if len(pair.Name) == 0 {
					return nil, problem.Invalid("source.form", "Источник: имя поля формы не должно быть пустым")
				}
				form = append(form, pair)
			}
			s.Form = form
			if err = catalog.ValidateFormHeaders(s.Headers, false); err != nil {
				return nil, problem.Invalid("source.headers", err.Error())
			}
		} else {
			s.Body, err = res.Expand(s.Body, true, nil)
		}
		if err != nil {
			return nil, err
		}
		s.Prefix, err = res.Expand(s.Prefix, false, nil)
		if err != nil {
			return nil, err
		}
		s.Suffix, err = res.Expand(s.Suffix, false, nil)
		if err != nil {
			return nil, err
		}
		if err = catalog.ValidateHeaderSource(*s); err != nil {
			return nil, problem.Invalid("source", err.Error())
		}
		if _, err = HeaderTLS(*s, res.Secrets); err != nil {
			return nil, err
		}
	}
	if err := catalog.ValidateRequestHeaders(headers); err != nil {
		return nil, problem.Invalid("headers", err.Error())
	}
	return headers, nil
}
