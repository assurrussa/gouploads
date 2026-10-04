package filepolicy

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net/http"
	"strings"
)

// DetectContentType sniffs the bytes, never the filename or supplied MIME type.
// net/http recognizes ID3 but not untagged MPEG Layer III frames. Keep that
// additional signature and WAV alias normalization shared across all ingress,
// TUS-prefix and finalization paths. This is format sniffing, not full decoding.
func DetectContentType(header []byte) string {
	if bytes.HasPrefix(header, []byte("ID3")) {
		if len(header) < 10 || header[3] < 2 || header[3] > 4 || header[4] == 0xff {
			return "application/octet-stream"
		}
		for _, value := range header[6:10] {
			if value&0x80 != 0 { // ID3 sizes are sync-safe integers.
				return "application/octet-stream"
			}
		}
		return mimeMP3
	}
	if mp3FrameHeader(header) {
		return mimeMP3
	}
	detected := http.DetectContentType(header)
	if strings.HasPrefix(detected, "audio/") {
		return NormalizeMIME(detected)
	}
	return detected
}

// ValidateAudioHeader rejects impossible/truncated container declarations. Size
// is the whole-file size, not the sniff prefix length. ExactReader separately
// verifies that the private object supplies every declared byte.
func ValidateAudioHeader(header []byte, contentType string, size int64) error {
	if size <= 0 {
		return nil // Unknown-size ingestion is checked again by finalization.
	}
	switch NormalizeMIME(contentType) {
	case mimeMP3:
		if err := validateMP3Size(header, size); err != nil {
			return err
		}
	case mimeWAV:
		if len(header) < 12 || !bytes.Equal(header[:4], []byte("RIFF")) || !bytes.Equal(header[8:12], []byte("WAVE")) {
			return errors.New("invalid WAVE header")
		}
		containerSize := int64(binary.LittleEndian.Uint32(header[4:8])) + 8
		if containerSize < 44 || size < containerSize {
			return errors.New("truncated WAVE container")
		}
	}
	return nil
}

func validateMP3Size(header []byte, size int64) error {
	var offset int64
	if bytes.HasPrefix(header, []byte("ID3")) {
		if len(header) < 10 {
			return errors.New("truncated ID3 header")
		}
		tagSize := int64(header[6])<<21 | int64(header[7])<<14 | int64(header[8])<<7 | int64(header[9])
		offset = 10 + tagSize
		if header[3] == 4 && header[5]&0x10 != 0 {
			offset += 10 // An ID3v2.4 footer is not included in its tag size.
		}
	}
	if size < offset+4 {
		return errors.New("MP3 has no complete frame header after its ID3 tag")
	}
	if offset+4 <= int64(len(header)) {
		length := mp3FrameSize(header[offset:])
		if length == 0 || size < offset+int64(length) {
			return errors.New("invalid or truncated MPEG Layer III frame")
		}
	}
	return nil
}

func mp3FrameHeader(header []byte) bool { return mp3FrameSize(header) != 0 }

func mp3FrameSize(header []byte) int {
	if len(header) < 4 || header[0] != 0xff || header[1]&0xe0 != 0xe0 {
		return 0
	}
	// MPEG version 01 and sample rate 11 are reserved. Layer III is 01;
	// free-format (0000) and invalid (1111) bit rates are not recognized.
	version, bitrate, sample := (header[1]>>3)&3, header[2]>>4, (header[2]>>2)&3
	if version == 1 || header[1]&0x06 != 0x02 || bitrate == 0 || bitrate == 15 || sample == 3 {
		return 0
	}
	rates := [3]int{44100, 48000, 32000}
	bitrates := [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	factor := 144000
	if version != 3 {
		bitrates = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
		factor = 72000
		for index := range rates {
			rates[index] /= 2
			if version == 0 {
				rates[index] /= 2
			}
		}
	}
	return factor*bitrates[bitrate]/rates[sample] + int((header[2]>>1)&1)
}
