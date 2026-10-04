// Package audiofixture supplies generated, non-user audio for regression tests.
package audiofixture

import _ "embed"

//go:embed testdata/tone.mp3
var MP3 []byte

//go:embed testdata/tone.wav
var WAV []byte

// UntaggedMP3 returns the same encoded MP3 frames without their ID3 metadata.
func UntaggedMP3() []byte {
	size := int(MP3[6])<<21 | int(MP3[7])<<14 | int(MP3[8])<<7 | int(MP3[9])
	return MP3[10+size:]
}
