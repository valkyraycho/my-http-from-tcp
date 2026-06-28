package headers

import (
	"bytes"
	"errors"
	"strings"
)

type Headers struct {
	headers map[string]string
}

var ErrMalformedFieldLine = errors.New("malformed field line")
var ErrMalformedFieldName = errors.New("malformed field name")

var CLRF = []byte("\r\n")

func NewHeaders() *Headers {
	return &Headers{
		headers: make(map[string]string),
	}
}

func (h *Headers) Get(key string) string {
	return h.headers[strings.ToLower(key)]
}

func (h *Headers) Set(key, value string) {
	h.headers[strings.ToLower(key)] = value
}

func isValidToken(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, r := range s {
		if !isTChar(r) {
			return false
		}
	}
	return true
}

func isTChar(r rune) bool {
	if r >= '0' && r <= '9' {
		return true
	}
	if r >= 'A' && r <= 'Z' {
		return true
	}
	if r >= 'a' && r <= 'z' {
		return true
	}

	switch r {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
	}
}

func (h *Headers) Parse(data []byte) (int, bool, error) {
	read := 0
	done := false

	for {
		idx := bytes.Index(data[read:], CLRF)
		if idx == -1 {
			break
		}

		if idx == 0 {
			done = true
			read += len(CLRF)
			break
		}

		key, value, err := parseHeader(data[read : read+idx])
		if err != nil {
			return 0, false, err
		}
		if !isValidToken(key) {
			return 0, false, ErrMalformedFieldName
		}

		read += idx + len(CLRF)
		h.Set(key, value)
	}

	return read, done, nil
}

func parseHeader(fieldLine []byte) (string, string, error) {
	parts := bytes.SplitN(fieldLine, []byte(":"), 2)
	if len(parts) != 2 {
		return "", "", ErrMalformedFieldLine
	}

	key := parts[0]
	value := bytes.TrimSpace(parts[1])

	if bytes.HasPrefix(key, []byte(" ")) || bytes.HasSuffix(key, []byte(" ")) {
		return "", "", ErrMalformedFieldName
	}

	return string(key), string(value), nil
}
