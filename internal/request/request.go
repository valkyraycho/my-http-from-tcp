package request

import "io"

type Request struct {
	RequestLine RequestLine
}

type RequestLine struct {
	HttpVersion string
	RequestTarget string
	Method string
}

func RequestFromReader(r io.Reader) (*Request, error) {
	return nil, nil
}