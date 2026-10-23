//go:build windows

package desktop

import (
	"reflect"
	"syscall"
	"testing"
)

func TestParseSelectedFiles(t *testing.T) {
	for _, tc := range []struct {
		parts []string
		want  []string
	}{
		{[]string{`C:\Users\Ada\one.txt`}, []string{`C:\Users\Ada\one.txt`}},
		{[]string{`C:\Users\Ada`, "one.txt", "two ☄.png"}, []string{`C:\Users\Ada\one.txt`, `C:\Users\Ada\two ☄.png`}},
		{nil, nil},
	} {
		var buf []uint16
		for _, part := range tc.parts {
			encoded, err := syscall.UTF16FromString(part)
			if err != nil {
				t.Fatal(err)
			}
			buf = append(buf, encoded...)
		}
		buf = append(buf, 0) // double-NUL termination
		if got := parseSelectedFiles(buf); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseSelectedFiles(%q) = %q, want %q", tc.parts, got, tc.want)
		}
	}
}
