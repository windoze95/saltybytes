package ai

import "testing"

func TestAudioFilePath(t *testing.T) {
	cases := []struct {
		format string
		want   string
	}{
		{"", "audio.webm"},     // empty defaults to webm
		{"webm", "audio.webm"}, // passthrough
		{"m4a", "audio.m4a"},   // common iOS format
		{"M4A", "audio.m4a"},   // case-insensitive
		{".mp3", "audio.mp3"},  // leading dot stripped
		{" wav ", "audio.wav"}, // whitespace trimmed
		{"exe", "audio.webm"},  // unsupported falls back to webm
		{"../x", "audio.webm"}, // path-ish input falls back to webm
		{"flac", "audio.flac"}, // remaining supported formats
		{"ogg", "audio.ogg"},
	}

	for _, tc := range cases {
		if got := audioFilePath(tc.format); got != tc.want {
			t.Errorf("audioFilePath(%q) = %q, want %q", tc.format, got, tc.want)
		}
	}
}
