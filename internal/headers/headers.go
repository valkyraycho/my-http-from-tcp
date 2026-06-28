package headers

import (
	"bytes"
	"errors"
)

type Headers map[string]string

var ErrMalformedFieldLine = errors.New("malformed field line")
var ErrMalformedFieldName = errors.New("malformed field name")

var CLRF = []byte("\r\n")

func NewHeaders() Headers {
	return make(Headers)
}

func (h Headers) Parse(data []byte) (int, bool, error) {
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

		read += idx + len(CLRF)
		h[key] = value
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
