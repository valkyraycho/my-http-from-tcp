package request

import (
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type chunkReader struct {
	data            string
	numBytesPerRead int
	pos             int
}

// Read reads up to len(p) or numBytesPerRead bytes from the string per call
// its useful for simulating reading a variable number of bytes per chunk from a network connection
func (cr *chunkReader) Read(p []byte) (n int, err error) {
	if cr.pos >= len(cr.data) {
		return 0, io.EOF
	}
	endIndex := min(cr.pos+cr.numBytesPerRead, len(cr.data))
	n = copy(p, cr.data[cr.pos:endIndex])
	cr.pos += n

	return n, nil
}

// TestRequestLineParse drives RequestFromReader end-to-end through the happy
// path and every malformed/unsupported branch of the request-line grammar.
//
// Each success case is asserted at a small chunk size (3 bytes per Read) so the
// incremental buffering path is exercised, not just the "whole message in one
// read" path. Chunk-size independence gets its own dedicated test below.
func TestRequestLineParse(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantErr     error // nil means "expect success"
		wantMethod  string
		wantTarget  string
		wantVersion string
	}{
		// ---- happy path -------------------------------------------------
		{
			name:        "GET root",
			input:       "GET / HTTP/1.1\r\nHost: localhost:42069\r\n\r\n",
			wantMethod:  "GET",
			wantTarget:  "/",
			wantVersion: "1.1",
		},
		{
			name:        "GET with path",
			input:       "GET /coffee HTTP/1.1\r\nHost: localhost:42069\r\n\r\n",
			wantMethod:  "GET",
			wantTarget:  "/coffee",
			wantVersion: "1.1",
		},
		{
			name:        "GET with query string",
			input:       "GET /coffee?size=large&milk=oat HTTP/1.1\r\n\r\n",
			wantMethod:  "GET",
			wantTarget:  "/coffee?size=large&milk=oat",
			wantVersion: "1.1",
		},
		{
			name:        "POST method",
			input:       "POST /submit HTTP/1.1\r\nHost: localhost\r\n\r\n",
			wantMethod:  "POST",
			wantTarget:  "/submit",
			wantVersion: "1.1",
		},
		{
			// The request line must parse correctly even when headers and a
			// body follow it; parse stops at StateDone and ignores the rest.
			name:        "request line followed by headers and body",
			input:       "DELETE /items/42 HTTP/1.1\r\nHost: localhost\r\nContent-Length: 5\r\n\r\nhello",
			wantMethod:  "DELETE",
			wantTarget:  "/items/42",
			wantVersion: "1.1",
		},

		// ---- malformed request line -------------------------------------
		{
			name:    "too many parts",
			input:   "GET / HTTP/1.1 extra\r\n\r\n",
			wantErr: ErrMalformedRequestLine,
		},
		{
			name:    "too few parts",
			input:   "GET /\r\n\r\n",
			wantErr: ErrMalformedRequestLine,
		},
		{
			name:    "double space between method and target",
			input:   "GET  / HTTP/1.1\r\n\r\n",
			wantErr: ErrMalformedRequestLine,
		},
		{
			name:    "version missing slash",
			input:   "GET / HTTP1.1\r\n\r\n",
			wantErr: ErrMalformedRequestLine,
		},
		{
			name:    "wrong protocol name",
			input:   "GET / TCP/1.1\r\n\r\n",
			wantErr: ErrMalformedRequestLine,
		},

		// ---- unsupported (well-formed but not 1.1) ----------------------
		{
			name:    "unsupported version 2.0",
			input:   "GET / HTTP/2.0\r\n\r\n",
			wantErr: ErrUnsupportedHttpVersion,
		},
		{
			name:    "unsupported version 1.0",
			input:   "GET / HTTP/1.0\r\n\r\n",
			wantErr: ErrUnsupportedHttpVersion,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &chunkReader{data: tt.input, numBytesPerRead: 3}
			r, err := RequestFromReader(reader)

			if tt.wantErr != nil {
				// %w wrapping in RequestFromReader lets ErrorIs see the sentinel.
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, r)
			assert.Equal(t, tt.wantMethod, r.RequestLine.Method)
			assert.Equal(t, tt.wantTarget, r.RequestLine.RequestTarget)
			assert.Equal(t, tt.wantVersion, r.RequestLine.HttpVersion)
		})
	}
}

// TestRequestFromReaderChunkSizes proves the parser is independent of how the
// stream is fragmented: the same request must parse identically whether it
// arrives one byte at a time or all at once. This is the property that makes
// the byte-counting/buffer-compaction logic worth having.
func TestRequestFromReaderChunkSizes(t *testing.T) {
	const input = "GET /coffee HTTP/1.1\r\nHost: localhost:42069\r\nUser-Agent: curl/7.81.0\r\nAccept: */*\r\n\r\n"

	for _, chunk := range []int{1, 2, 3, 7, 16, len(input)} {
		t.Run(fmt.Sprintf("chunk=%d", chunk), func(t *testing.T) {
			reader := &chunkReader{data: input, numBytesPerRead: chunk}
			r, err := RequestFromReader(reader)

			require.NoError(t, err)
			require.NotNil(t, r)
			assert.Equal(t, "GET", r.RequestLine.Method)
			assert.Equal(t, "/coffee", r.RequestLine.RequestTarget)
			assert.Equal(t, "1.1", r.RequestLine.HttpVersion)
		})
	}
}

// TestRequestFromReaderEmpty: a reader that yields no bytes before EOF cannot
// produce a request line, so RequestFromReader must surface an error rather
// than returning a zero-value Request.
func TestRequestFromReaderEmpty(t *testing.T) {
	reader := &chunkReader{data: "", numBytesPerRead: 3}
	r, err := RequestFromReader(reader)

	require.Error(t, err)
	assert.Nil(t, r)
}

// TestRequestFromReaderIncompleteRequestLine: the stream ends (EOF) before the
// request line is terminated by CRLF. A truncated request is not a valid
// request, so this must be an error rather than a silently-empty success.
func TestRequestFromReaderIncompleteRequestLine(t *testing.T) {
	reader := &chunkReader{data: "GET / HTTP/1.1", numBytesPerRead: 3}
	r, err := RequestFromReader(reader)

	require.Error(t, err)
	assert.Nil(t, r)
}

// TestParseStateMachine is a white-box test (same package) for the parse()
// state machine itself, exercising transitions the public API can't reach:
//   - incomplete input returns (0, nil) and leaves state untouched, so the
//     caller knows to read more;
//   - a completed parse consumes exactly the request-line bytes and then
//     treats further input as a no-op;
//   - once a parse errors, the request is poisoned and rejects further input.
func TestParseStateMachine(t *testing.T) {
	t.Run("incomplete input asks for more without erroring", func(t *testing.T) {
		req := newRequest()
		n, err := req.parse([]byte("GET / HTTP/1.1")) // no CRLF yet

		require.NoError(t, err)
		assert.Equal(t, 0, n)
		assert.Equal(t, StateInit, req.state)
		assert.Equal(t, RequestLine{}, req.RequestLine)
	})

	t.Run("complete line consumes only the request line", func(t *testing.T) {
		req := newRequest()
		// 16 bytes: "GET / HTTP/1.1" (14) + CRLF (2). The trailing "extra"
		// belongs to headers/body and must NOT be consumed here.
		n, err := req.parse([]byte("GET / HTTP/1.1\r\nextra"))

		require.NoError(t, err)
		assert.Equal(t, 16, n)
		assert.Equal(t, StateDone, req.state)
		assert.Equal(t, "GET", req.RequestLine.Method)

		// Once done, further parse calls are a no-op (already complete).
		n2, err := req.parse([]byte("anything"))
		require.NoError(t, err)
		assert.Equal(t, 0, n2)
	})

	t.Run("errored request rejects further parsing", func(t *testing.T) {
		req := newRequest()
		_, err := req.parse([]byte("GET / TCP/1.1\r\n")) // wrong protocol
		require.ErrorIs(t, err, ErrMalformedRequestLine)
		assert.Equal(t, StateError, req.state)

		// A poisoned request must not silently accept a valid line afterward.
		_, err = req.parse([]byte("GET / HTTP/1.1\r\n"))
		require.ErrorIs(t, err, ErrRequestInErrorState)
	})
}
