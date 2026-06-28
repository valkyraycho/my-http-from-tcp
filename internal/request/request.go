package request

import (
	"bytes"
	"errors"
	"fmt"
	"io"
)

type parserState string

const (
	StateInit  parserState = "init"
	StateDone  parserState = "done"
	StateError parserState = "error"
)

type Request struct {
	RequestLine RequestLine
	state       parserState
}

func (r *Request) isDone() bool {
	return r.state == StateDone || r.state == StateError
}

type RequestLine struct {
	HttpVersion   string
	RequestTarget string
	Method        string
}

var ErrMalformedRequestLine = errors.New("malformed request line")
var ErrUnsupportedHttpVersion = errors.New("unsupported HTTP version")
var ErrRequestInErrorState = errors.New("request in error state")

var SEPARATOR = []byte("\r\n")

func RequestFromReader(r io.Reader) (*Request, error) {
	req := newRequest()

	// NOTE: a buffer could get overflowed if the request is too large
	buf := make([]byte, 1024)
	bufLen := 0

	for !req.isDone() {
		n, err := r.Read(buf[bufLen:])
		if err != nil {
			if err == io.EOF && bufLen > 0 {
				break
			}
			return nil, fmt.Errorf("failed to read from reader: %w", err)
		}
		bufLen += n

		readN, err := req.parse(buf[:bufLen])

		if err != nil {
			return nil, fmt.Errorf("failed to parse request: %w", err)
		}

		copy(buf, buf[readN:bufLen])
		bufLen -= readN
	}

	return req, nil
}

func (r *Request) parse(data []byte) (int, error) {
	read := 0

outer:
	for {
		switch r.state {
		case StateInit:
			rl, n, err := parseRequestLine(data[read:])
			if err != nil {
				r.state = StateError
				return 0, err
			}

			if n == 0 {
				break outer
			}

			r.RequestLine = *rl
			read += n
			r.state = StateDone

		case StateError:
			return 0, ErrRequestInErrorState
		case StateDone:
			break outer
		}
	}
	return read, nil
}

func newRequest() *Request {
	return &Request{
		state: StateInit,
	}
}

func parseRequestLine(b []byte) (*RequestLine, int, error) {
	startLine, _, ok := bytes.Cut(b, SEPARATOR)
	if !ok {
		return nil, 0, nil
	}
	read := len(startLine) + len(SEPARATOR)

	startLineParts := bytes.Split(startLine, []byte(" "))
	if len(startLineParts) != 3 {
		return nil, 0, ErrMalformedRequestLine
	}

	httpParts := bytes.Split(startLineParts[2], []byte("/"))
	if len(httpParts) != 2 || string(httpParts[0]) != "HTTP" {
		return nil, 0, ErrMalformedRequestLine
	}

	if string(httpParts[1]) != "1.1" {
		return nil, 0, ErrUnsupportedHttpVersion
	}

	rl := &RequestLine{
		Method:        string(startLineParts[0]),
		RequestTarget: string(startLineParts[1]),
		HttpVersion:   string(httpParts[1]),
	}

	return rl, read, nil
}
