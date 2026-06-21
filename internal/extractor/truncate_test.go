// Copyright 2026 Omar Almahri and the Quert contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package extractor

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		max      int
		want     string
		wantRune int
	}{
		{"empty", "", 5, "", 0},
		{"ascii under limit", "hello", 10, "hello", 5},
		{"ascii at limit", "hello", 5, "hello", 5},
		{"ascii over limit", "hello world", 5, "hello", 5},
		{"zero max returns whole", "hello", 0, "hello", 5},
		{"negative max returns whole", "hello", -1, "hello", 5},
		// 5 multi-byte runes (each 3 bytes in UTF-8). Byte-slicing at 3 would
		// split the second rune; rune-safe truncation must keep 2 whole runes.
		{"cjk over limit", "日本語のテスト", 2, "日本", 2},
		{"cjk at limit", "日本", 2, "日本", 2},
		{"emoji over limit", "👍👍👍", 2, "👍👍", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateRunes(tt.in, tt.max)
			assert.Equal(t, tt.want, got)
			assert.True(t, utf8.ValidString(got), "result must be valid UTF-8")
			assert.Equal(t, tt.wantRune, utf8.RuneCountInString(got))
		})
	}
}

// TestCleanTextContent_NoRuneCorruption guards the actual extractor path: a
// MaxTextLength cap on multi-byte text must never produce invalid UTF-8.
func TestCleanTextContent_NoRuneCorruption(t *testing.T) {
	config := GetDefaultExtractorConfig()
	config.MaxTextLength = 5 // 5 runes
	config.NormalizeWhitespace = false
	h := &HTMLContentExtractor{Config: config}

	got := h.CleanTextContent("日本語のテスト")
	assert.True(t, utf8.ValidString(got), "cleaned text must be valid UTF-8")
	assert.Equal(t, 5, utf8.RuneCountInString(got))
	assert.Equal(t, "日本語のテ", got)
}
