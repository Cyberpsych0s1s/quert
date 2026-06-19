package extractor

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestDecodeToUTF8(t *testing.T) {
	// "café" encoded as ISO-8859-1: the é is a single byte 0xE9.
	latin1 := []byte{'c', 'a', 'f', 0xE9}
	assert.False(t, utf8.Valid(latin1), "precondition: raw latin-1 is not valid UTF-8")

	got := decodeToUTF8(latin1, "text/html; charset=iso-8859-1")
	assert.True(t, utf8.Valid(got), "decoded output must be valid UTF-8")
	assert.Equal(t, "café", string(got))
}

func TestDecodeToUTF8_MetaCharsetSniff(t *testing.T) {
	// No charset in Content-Type; declared via <meta> with windows-1252 (0x93/0x94
	// are smart quotes there). charset.NewReader sniffs the meta tag.
	html := append([]byte(`<html><head><meta charset="windows-1252"></head><body>`), 0x93, 'h', 'i', 0x94)
	html = append(html, []byte(`</body></html>`)...)

	got := decodeToUTF8(html, "text/html")
	assert.True(t, utf8.Valid(got), "decoded output must be valid UTF-8")
	assert.Contains(t, string(got), "“hi”", "smart quotes decoded to UTF-8")
}

func TestDecodeToUTF8_AlreadyUTF8(t *testing.T) {
	utf8In := []byte("日本語 café")
	got := decodeToUTF8(utf8In, "text/html; charset=utf-8")
	assert.Equal(t, utf8In, got)
}
