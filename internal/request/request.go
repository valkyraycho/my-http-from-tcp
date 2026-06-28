package request

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/valkyraycho/my-http-from-tcp/internal/headers"
)

type parserState string

const (
	StateInit    parserState = "init"
	StateHeaders parserState = "headers"
	StateDone    parserState = "done"
	StateError   parserState = "error"
)

type Request struct {
	RequestLine RequestLine
	Headers     *headers.Headers
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
var ErrIncompleteRequest = errors.New("incomplete request: stream ended before request was fully parsed")

var SEPARATOR = []byte("\r\n")

func RequestFromReader(r io.Reader) (*Request, error) {
	req := newRequest()

	// NOTE: a buffer could get overflowed if the request is too large
	buf := make([]byte, 1024)
	bufLen := 0

	for !req.isDone() {
		n, err := r.Read(buf[bufLen:])
		// Count the bytes first: a reader may legally return data alongside
		// io.EOF in the same call, and those bytes still need to be parsed.
		bufLen += n

		readN, parseErr := req.parse(buf[:bufLen])
		if parseErr != nil {
			return nil, fmt.Errorf("failed to parse request: %w", parseErr)
		}
		copy(buf, buf[readN:bufLen])
		bufLen -= readN

		if err != nil {
			// EOF means no more data will ever arrive; stop reading and let
			// the post-loop invariant decide whether what we got was complete.
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to read from reader: %w", err)
		}
	}

	// The loop can exit because the stream hit EOF mid-parse. If the parser
	// never reached a terminal state, the request was truncated. Funneling
	// every "stream ended early" path through this single check keeps the
	// truncation error consistent regardless of where the bytes ran out.
	if !req.isDone() {
		return nil, ErrIncompleteRequest
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
			r.state = StateHeaders
		case StateHeaders:
			n, done, err := r.Headers.Parse(data[read:])
			if err != nil {
				r.state = StateError
				return 0, err
			}

			if n == 0 {
				break outer
			}

			read += n
			if done {
				r.state = StateDone
			}

		case StateError:
			return 0, ErrRequestInErrorState
		case StateDone:
			break outer
		default:
			panic(fmt.Sprintf("unexpected parser state: %s", r.state))
		}
	}
	return read, nil
}

func newRequest() *Request {
	return &Request{
		Headers: headers.NewHeaders(),
		state:   StateInit,
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
