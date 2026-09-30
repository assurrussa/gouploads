package filepolicy

import (
	"bytes"
	"encoding/binary"
)

// IncompleteMIMEHeader identifies a prefix that can still become a supported
// MIME signature. It never approves content; the caller must inspect more bytes.
func IncompleteMIMEHeader(header []byte, contentType string) bool {
	var signatures [][]byte
	switch NormalizeMIME(contentType) {
	case "image/png":
		signatures = [][]byte{[]byte("\x89PNG\r\n\x1a\n")}
	case mimeJPEG:
		signatures = [][]byte{{0xff, 0xd8, 0xff}}
	case "image/gif":
		signatures = [][]byte{[]byte("GIF87a"), []byte("GIF89a")}
	case "application/pdf":
		signatures = [][]byte{[]byte("%PDF-")}
	case "video/webm":
		signatures = [][]byte{{0x1a, 0x45, 0xdf, 0xa3}}
	case "image/webp":
		return incompleteWebP(header)
	case "video/mp4":
		return incompleteMP4(header)
	default:
		return false
	}
	for _, signature := range signatures {
		if len(header) < len(signature) && bytes.HasPrefix(signature, header) {
			return true
		}
	}
	return false
}

func incompleteWebP(header []byte) bool {
	const signature = "RIFF\x00\x00\x00\x00WEBPVP"
	if len(header) >= len(signature) {
		return false
	}
	for index, value := range header {
		if index >= 4 && index < 8 {
			continue // RIFF size bytes do not form part of the MIME signature.
		}
		if value != signature[index] {
			return false
		}
	}
	return true
}

func incompleteMP4(header []byte) bool {
	if len(header) < 4 {
		return len(header) == 0 || header[0] == 0
	}
	boxSize := binary.BigEndian.Uint32(header[:4])
	if boxSize < 12 || boxSize > 512 || boxSize%4 != 0 || uint32(len(header)) >= boxSize {
		return false
	}
	brandPrefix := header[4:min(len(header), 8)]
	return bytes.Equal(brandPrefix, []byte("ftyp")[:len(brandPrefix)])
}
