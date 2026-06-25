package request

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

type Request struct {
	RequestLine RequestLine
}

type RequestLine struct {
	HttpVersion   string
	RequestTarget string
	Method        string
}

var ErrMalformedRequestLine = errors.New("malformed request line")
var ErrUnsupportedHttpVersion = errors.New("unsupported HTTP version")

const SEPARATOR = "\r\n"

func parseRequestLine(s string) (*RequestLine, string, error) {
	idx := strings.Index(s, SEPARATOR)
	if idx == -1 {
		return nil, "", nil
	}

	startLine := s[:idx]
	restOfMessage := s[idx+len(SEPARATOR):]
	parts := strings.Split(startLine, " ")
	if len(parts) != 3 {
		return nil, "", ErrMalformedRequestLine
	}

	httpParts := strings.Split(parts[2], "/")
	if len(httpParts) != 2 || httpParts[0] != "HTTP" || httpParts[1] != "1.1" {
		return nil, "", ErrUnsupportedHttpVersion
	}

	rl := &RequestLine{
		Method:        parts[0],
		RequestTarget: parts[1],
		HttpVersion:   httpParts[1],
	}

	return rl, restOfMessage, nil
}

func RequestFromReader(r io.Reader) (*Request, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read request: %w", err)
	}

	s := string(data)
	rl, _, err := parseRequestLine(s)
	if err != nil {
		return nil, fmt.Errorf("failed to parse request line: %w", err)
	}

	if rl == nil {
		return nil, fmt.Errorf("request line is empty")
	}

	req := &Request{
		RequestLine: *rl,
	}
	return req, nil
}
