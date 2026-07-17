package mailparse

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime/quotedprintable"
	"strings"
	"unicode/utf8"

	"github.com/emersion/go-message/charset"
)

// TextResult is a decoded text part.
type TextResult struct {
	Text            string
	Truncated       bool
	CharsetFallback bool
}

// DecodeText decodes a text part payload: content-transfer-encoding, then
// charset to UTF-8, then a character cap. preTruncated marks payloads already
// cut short before transfer, so base64 input is trimmed to a 4-byte boundary
// and decoder errors at the cut are tolerated.
func DecodeText(raw []byte, encoding, charsetLabel string, preTruncated bool, maxChars int) TextResult {
	decoded := decodeCTE(raw, encoding, preTruncated)
	res := TextResult{Truncated: preTruncated}

	text, fallback := toUTF8(decoded, charsetLabel)
	res.CharsetFallback = fallback

	runes := []rune(text)
	if maxChars > 0 && len(runes) > maxChars {
		text = string(runes[:maxChars])
		res.Truncated = true
	}
	res.Text = text
	return res
}

// DecodeBinary decodes an attachment payload from its content-transfer-encoding.
func DecodeBinary(raw []byte, encoding string) ([]byte, error) {
	switch strings.ToLower(encoding) {
	case "base64":
		data, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, newBase64Cleaner(raw)))
		if err != nil {
			return nil, fmt.Errorf("decoding base64 content: %w", err)
		}
		return data, nil
	case "quoted-printable":
		data, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw)))
		if err != nil {
			return nil, fmt.Errorf("decoding quoted-printable content: %w", err)
		}
		return data, nil
	default: // 7bit, 8bit, binary, empty
		return raw, nil
	}
}

func decodeCTE(raw []byte, encoding string, tolerant bool) []byte {
	switch strings.ToLower(encoding) {
	case "base64":
		cleaned := cleanBase64(raw)
		if tolerant {
			cleaned = cleaned[:len(cleaned)-len(cleaned)%4]
		}
		decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(cleaned)))
		if err != nil && !tolerant {
			return raw
		}
		return decoded // best effort: everything readable before any error
	case "quoted-printable":
		decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw)))
		if err != nil && !tolerant && len(decoded) == 0 {
			return raw
		}
		return decoded
	default:
		return raw
	}
}

// toUTF8 converts data from charsetLabel to valid UTF-8. The bool reports any
// lossy outcome: unknown/broken charset, or invalid bytes replaced.
func toUTF8(data []byte, charsetLabel string) (string, bool) {
	label := strings.ToLower(strings.TrimSpace(charsetLabel))
	if label == "" || label == "utf-8" || label == "us-ascii" || label == "ascii" {
		return sanitizeUTF8(data)
	}
	r, err := charset.Reader(label, bytes.NewReader(data))
	if err != nil {
		s, _ := sanitizeUTF8(data)
		return s, true
	}
	converted, err := io.ReadAll(r)
	if err != nil {
		s, _ := sanitizeUTF8(data)
		return s, true
	}
	return sanitizeUTF8(converted)
}

// sanitizeUTF8 guarantees valid UTF-8 so results survive JSON encoding, and
// reports whether any bytes had to be replaced.
func sanitizeUTF8(data []byte) (string, bool) {
	if utf8.Valid(data) {
		return string(data), false
	}
	return strings.ToValidUTF8(string(data), "�"), true
}

// cleanBase64 strips whitespace so line-wrapped base64 decodes.
func cleanBase64(raw []byte) []byte {
	cleaned := make([]byte, 0, len(raw))
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\r', '\n':
		default:
			cleaned = append(cleaned, b)
		}
	}
	return cleaned
}

func newBase64Cleaner(raw []byte) io.Reader {
	return bytes.NewReader(cleanBase64(raw))
}
