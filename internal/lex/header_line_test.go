package lex

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestAdviceNamesHeaderLine(t *testing.T) {
	tests := []struct {
		name, original string
		edit           func([]byte) []byte
		want           string
	}{
		{
			name:     "копия шапки в конце",
			original: "# A\n\n- one\n",
			edit: func(data []byte) []byte {
				header, body, _ := bytes.Cut(data, []byte("\n"))
				return append(append(body, header...), '\n')
			},
			want: "text above lex header (found at line 4); move the text below the header, or if the header line was deleted, restore it as the first line",
		},
		{
			name:     "строка над шапкой",
			original: "# A\n\n- one\n",
			edit: func(data []byte) []byte {
				return append([]byte("intro\n"), data...)
			},
			want: "text above lex header (found at line 2); move the text below the header, or if the header line was deleted, restore it as the first line",
		},
		{
			name:     "front matter",
			original: "---\nname: a\n---\n# A\n\n- one\n",
			edit: func(data []byte) []byte {
				start := bytes.Index(data, []byte("<!-- lex: собран из "))
				end := bytes.IndexByte(data[start:], '\n') + start + 1
				header := append([]byte(nil), data[start:end]...)
				body := append([]byte(nil), data[:start]...)
				body = append(body, data[end:]...)
				return append(body, header...)
			},
			want: "text above lex header (found at line 7); move the text below the header, or if the header line was deleted, restore it immediately after the front matter",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, out, codex, _, data := builtWithHeader(t, tt.original)
			edited := tt.edit(data)
			mustWrite(t, out, edited)

			absorbErr := Absorb(codex, out, AbsorbOptions{})
			if absorbErr == nil || !strings.Contains(absorbErr.Error(), tt.want) {
				t.Fatalf("absorb error = %v, want it to contain %q", absorbErr, tt.want)
			}
			check, checkErr := CheckWithWarnings(codex, CheckOptions{})
			if checkErr == nil || !strings.Contains(checkErr.Error(), tt.want) {
				t.Fatalf("check advice = %q, error = %v; want it to contain %q", check, checkErr, tt.want)
			}
			status, statusErr := Status(codex)
			if statusErr != nil {
				t.Fatal(statusErr)
			}
			if len(status) != 1 || status[0].Refusal != tt.want {
				t.Fatalf("status refusal = %s, want %s", fmt.Sprint(status), tt.want)
			}
		})
	}
}
