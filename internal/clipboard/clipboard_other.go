//go:build !windows

package clipboard

import (
	"fmt"
	"qag/internal/reporting"
)

func Available() bool { return false }
func Write(content reporting.RichContent) error {
	return fmt.Errorf("native clipboard доступен в Windows; используйте browser HTML clipboard")
}

func WriteText(text string) error { return Write(reporting.RichContent{Text: text}) }
