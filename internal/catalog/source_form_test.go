package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

func formSource() HTTPHeaderSource {
	return HTTPHeaderSource{Method: "POST", URL: "https://example.test/token", BodyMode: "form", TLS: TLSConfig{Mode: "tls"}, JSONPointer: "/access_token", FetchMode: "per_run"}
}

func TestSourceFormEncodingAndValidation(t *testing.T) {
	s := formSource()
	s.Form = []KeyValue{{"grant_type", "client_credentials"}, {"client_secret", "a+b&c= d%"}, {"scope", "проверка"}, {"scope", ""}, {"", ""}}
	body, err := EncodeSourceForm(s.Form)
	if err != nil || body != "grant_type=client_credentials&client_secret=a%2Bb%26c%3D+d%25&scope=%D0%BF%D1%80%D0%BE%D0%B2%D0%B5%D1%80%D0%BA%D0%B0&scope=" {
		t.Fatalf("%q: %v", body, err)
	}
	if err := ValidateHeaderSource(s); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*HTTPHeaderSource){
		"value without name": func(s *HTTPHeaderSource) { s.Form = []KeyValue{{"", "bad"}} },
		"mixed body":         func(s *HTTPHeaderSource) { s.Body = "not a form" },
		"inactive form":      func(s *HTTPHeaderSource) { s.BodyMode = "text" },
		"wrong content type": func(s *HTTPHeaderSource) { s.Headers = []KeyValue{{"Content-Type", "application/json"}} },
		"duplicate content type": func(s *HTTPHeaderSource) {
			s.Headers = []KeyValue{{"Content-Type", "application/x-www-form-urlencoded"}, {"content-type", "application/x-www-form-urlencoded"}}
		},
		"charset": func(s *HTTPHeaderSource) {
			s.Headers = []KeyValue{{"Content-Type", "application/x-www-form-urlencoded; charset=latin1"}}
		},
		"suffix newline": func(s *HTTPHeaderSource) { s.Suffix = "\r\nbad" },
		"prefix newline": func(s *HTTPHeaderSource) { s.Prefix = "\nbad" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := s
			change(&copy)
			if ValidateHeaderSource(copy) == nil {
				t.Fatal("accepted invalid source")
			}
		})
	}
	s.Headers = []KeyValue{{"Content-Type", "application/x-www-form-urlencoded; charset=UTF-8"}}
	if err := ValidateHeaderSource(s); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyHeaderSourceKeepsRawBodyAndPrefix(t *testing.T) {
	var s HTTPHeaderSource
	if err := json.Unmarshal([]byte(`{"method":"POST","url":"https://example.test/token","query":[],"headers":[],"bodyMode":"text","body":"secret=a%2Bb","tls":{"mode":"tls"},"jsonPointer":"/token","prefix":"Bearer ","fetchMode":"per_run"}`), &s); err != nil {
		t.Fatal(err)
	}
	if err := ValidateHeaderSource(s); err != nil {
		t.Fatal(err)
	}
	if s.Suffix != "" || len(s.Form) != 0 || s.Body != "secret=a%2Bb" || s.Prefix != "Bearer " {
		t.Fatalf("changed legacy source: %+v", s)
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), `"suffix"`) || strings.Contains(string(b), `"form"`) {
		t.Fatalf("nonempty new defaults: %s", b)
	}
}
