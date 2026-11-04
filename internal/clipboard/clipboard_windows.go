//go:build windows

package clipboard

import (
	"fmt"
	"runtime"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"github.com/SardelkaS/qag/internal/reporting"
)

var mu sync.Mutex
var user32 = windows.NewLazySystemDLL("user32.dll")
var kernel32 = windows.NewLazySystemDLL("kernel32.dll")

func Available() bool {
	station, _, _ := user32.NewProc("GetProcessWindowStation").Call()
	if station == 0 {
		return false
	}
	flags := struct{ Inherit, Reserved, Flags uint32 }{}
	var needed uint32
	result, _, _ := user32.NewProc("GetUserObjectInformationW").Call(station, 1, uintptr(unsafe.Pointer(&flags)), unsafe.Sizeof(flags), uintptr(unsafe.Pointer(&needed)))
	return result != 0 && flags.Flags&1 != 0
}
func htmlClipboard(fragment string) []byte {
	body := "<html><body><!--StartFragment-->" + fragment + "<!--EndFragment--></body></html>"
	template := "Version:0.9\r\nStartHTML:%010d\r\nEndHTML:%010d\r\nStartFragment:%010d\r\nEndFragment:%010d\r\n"
	offset := len(fmt.Sprintf(template, 0, 0, 0, 0))
	start := offset + len("<html><body><!--StartFragment-->")
	end := start + len(fragment)
	header := fmt.Sprintf(template, offset, offset+len(body), start, end)
	return append([]byte(header+body), 0)
}
func Write(content reporting.RichContent) error {
	return write(content, false)
}

func WriteText(text string) error {
	return write(reporting.RichContent{Text: text}, true)
}

func write(content reporting.RichContent, plain bool) error {
	mu.Lock()
	defer mu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	register := user32.NewProc("RegisterClipboardFormatW")
	format := func(name string) (uintptr, error) {
		text, _ := windows.UTF16PtrFromString(name)
		id, _, err := register.Call(uintptr(unsafe.Pointer(text)))
		if id == 0 {
			return 0, err
		}
		return id, nil
	}
	htmlID, err := format("HTML Format")
	if err != nil {
		return err
	}
	rtfID, err := format("Rich Text Format")
	if err != nil {
		return err
	}
	units := append(utf16.Encode([]rune(content.Text)), 0)
	unicodeBytes := make([]byte, len(units)*2)
	for i, u := range units {
		unicodeBytes[2*i] = byte(u)
		unicodeBytes[2*i+1] = byte(u >> 8)
	}
	values := []struct {
		format uintptr
		data   []byte
		handle uintptr
	}{{rtfID, append([]byte(content.RTF), 0), 0}, {htmlID, htmlClipboard(content.HTML), 0}, {13, unicodeBytes, 0}}
	if plain {
		values = values[2:]
	}
	alloc := kernel32.NewProc("GlobalAlloc")
	lock := kernel32.NewProc("GlobalLock")
	unlock := kernel32.NewProc("GlobalUnlock")
	free := kernel32.NewProc("GlobalFree")
	copyMemory := kernel32.NewProc("RtlMoveMemory")
	defer func() {
		for _, v := range values {
			if v.handle != 0 {
				free.Call(v.handle)
			}
		}
	}()
	for i := range values {
		handle, _, e := alloc.Call(0x0002, uintptr(len(values[i].data)))
		if handle == 0 {
			return fmt.Errorf("clipboard allocation: %v", e)
		}
		values[i].handle = handle
		ptr, _, e := lock.Call(handle)
		if ptr == 0 {
			return fmt.Errorf("clipboard memory: %v", e)
		}
		copyMemory.Call(ptr, uintptr(unsafe.Pointer(&values[i].data[0])), uintptr(len(values[i].data)))
		runtime.KeepAlive(values[i].data)
		unlock.Call(handle)
	}
	opened := false
	class, _ := windows.UTF16PtrFromString("STATIC")
	window, _, createErr := user32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, ^uintptr(2), 0, 0, 0)
	if window == 0 {
		return fmt.Errorf("clipboard owner window: %v", createErr)
	}
	defer user32.NewProc("DestroyWindow").Call(window)
	for i := 0; i < 10; i++ {
		result, _, _ := user32.NewProc("OpenClipboard").Call(window)
		if result != 0 {
			opened = true
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !opened {
		return fmt.Errorf("буфер обмена занят другим приложением")
	}
	defer user32.NewProc("CloseClipboard").Call()
	if result, _, e := user32.NewProc("EmptyClipboard").Call(); result == 0 {
		return fmt.Errorf("clipboard clear: %v", e)
	}
	for i := range values {
		result, _, e := user32.NewProc("SetClipboardData").Call(values[i].format, values[i].handle)
		if result == 0 {
			return fmt.Errorf("clipboard publish: %v", e)
		}
		values[i].handle = 0
	}
	return nil
}
