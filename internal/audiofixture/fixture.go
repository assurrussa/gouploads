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

// MP3WithTag adds valid ID3v2.3 padding before the real encoded audio frames.
func MP3WithTag(size int) []byte {
	header := [10]byte{
		'I', 'D', '3', 3, 0, 0, byte((size >> 21) & 0x7f), byte((size >> 14) & 0x7f),
		byte((size >> 7) & 0x7f), byte(size & 0x7f),
	}
	frames := UntaggedMP3()
	body := make([]byte, len(header)+size+len(frames))
	copy(body, header[:])
	copy(body[len(header)+size:], frames)
	return body
}
