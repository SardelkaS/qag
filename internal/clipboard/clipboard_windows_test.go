//go:build windows

package clipboard

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestHTMLClipboardOffsetsCountUTF8Bytes(t *testing.T) {
	fragment := "<p>Привет 🧪</p>"
	raw := string(htmlClipboard(fragment))
	get := func(name string) int {
		value := regexp.MustCompile(name + `:(\d+)`).FindStringSubmatch(raw)
		if len(value) != 2 {
			t.Fatal("missing offset")
		}
		n, _ := strconv.Atoi(value[1])
		return n
	}
	start, end := get("StartFragment"), get("EndFragment")
	if raw[start:end] != fragment {
		t.Fatal("UTF-8 offset mismatch")
	}
	if !strings.HasPrefix(raw[get("StartHTML"):], "<html>") || raw[get("EndHTML"):] != "\x00" {
		t.Fatal("HTML bounds mismatch")
	}
}
