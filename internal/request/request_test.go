package request

import (
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkyraycho/my-http-from-tcp/internal/headers"
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
//   - parsing the request line advances to StateHeaders (not StateDone) and
//     consumes exactly the request-line bytes when no header lines follow yet;
//   - the empty terminator line drives StateHeaders -> StateDone;
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

	t.Run("request line advances to header state", func(t *testing.T) {
		req := newRequest()
		// 16 bytes: "GET / HTTP/1.1" (14) + CRLF (2). "extra" has no CRLF, so
		// it's an incomplete header line: parse leaves it for the next call.
		n, err := req.parse([]byte("GET / HTTP/1.1\r\nextra"))

		require.NoError(t, err)
		assert.Equal(t, 16, n)
		assert.Equal(t, StateHeaders, req.state) // not done: headers still pending
		assert.Equal(t, "GET", req.RequestLine.Method)
	})

	t.Run("terminator line transitions headers to done", func(t *testing.T) {
		req := newRequest()
		// Request line, then immediately the empty line that ends the headers.
		n, err := req.parse([]byte("GET / HTTP/1.1\r\n\r\n"))

		require.NoError(t, err)
		assert.Equal(t, 18, n)
		assert.Equal(t, StateDone, req.state)

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

// TestRequestHeadersParse covers the integration between the request parser and
// the header sub-parser: RequestFromReader must drive Headers.Parse to
// completion, expose the parsed headers on Request.Headers, and (because both
// stages share one buffered loop) work regardless of how the stream is chunked.
func TestRequestHeadersParse(t *testing.T) {
	t.Run("standard headers", func(t *testing.T) {
		reader := &chunkReader{
			data:            "GET / HTTP/1.1\r\nHost: localhost:42069\r\nUser-Agent: curl/7.81.0\r\nAccept: */*\r\n\r\n",
			numBytesPerRead: 3,
		}
		r, err := RequestFromReader(reader)
		require.NoError(t, err)
		require.NotNil(t, r)
		require.NotNil(t, r.Headers)

		assert.Equal(t, "localhost:42069", r.Headers.Get("Host"))
		assert.Equal(t, "curl/7.81.0", r.Headers.Get("User-Agent"))
		assert.Equal(t, "*/*", r.Headers.Get("Accept"))
	})

	t.Run("header lookup is case-insensitive", func(t *testing.T) {
		reader := &chunkReader{
			data:            "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n",
			numBytesPerRead: 5,
		}
		r, err := RequestFromReader(reader)
		require.NoError(t, err)
		assert.Equal(t, "example.com", r.Headers.Get("host"))
		assert.Equal(t, "example.com", r.Headers.Get("HOST"))
	})

	t.Run("repeated headers fold into one comma-separated value", func(t *testing.T) {
		// Verifies the request parser inherits Headers' RFC 7230 value folding.
		reader := &chunkReader{
			data:            "GET / HTTP/1.1\r\nAccept: text/html\r\nAccept: application/json\r\n\r\n",
			numBytesPerRead: 7,
		}
		r, err := RequestFromReader(reader)
		require.NoError(t, err)
		assert.Equal(t, "text/html, application/json", r.Headers.Get("Accept"))
	})

	t.Run("request with no headers", func(t *testing.T) {
		// Request line immediately followed by the empty terminator line.
		reader := &chunkReader{
			data:            "GET / HTTP/1.1\r\n\r\n",
			numBytesPerRead: 4,
		}
		r, err := RequestFromReader(reader)
		require.NoError(t, err)
		require.NotNil(t, r.Headers)
		assert.Equal(t, "", r.Headers.Get("Host"), "no headers means empty lookups")
		assert.Equal(t, "GET", r.RequestLine.Method, "request line still parsed")
	})

	t.Run("headers parse identically across chunk sizes", func(t *testing.T) {
		const input = "GET /coffee HTTP/1.1\r\nHost: localhost:42069\r\nUser-Agent: curl/7.81.0\r\nAccept: */*\r\n\r\n"

		for _, chunk := range []int{1, 2, 3, 7, 16, len(input)} {
			t.Run(fmt.Sprintf("chunk=%d", chunk), func(t *testing.T) {
				reader := &chunkReader{data: input, numBytesPerRead: chunk}
				r, err := RequestFromReader(reader)
				require.NoError(t, err)
				assert.Equal(t, "localhost:42069", r.Headers.Get("Host"))
				assert.Equal(t, "curl/7.81.0", r.Headers.Get("User-Agent"))
				assert.Equal(t, "*/*", r.Headers.Get("Accept"))
			})
		}
	})

	t.Run("malformed header name propagates as an error", func(t *testing.T) {
		// "H@st" contains '@', not a valid tchar, so Headers.Parse rejects it.
		// That error must bubble up and fail the whole request.
		reader := &chunkReader{
			data:            "GET / HTTP/1.1\r\nH@st: localhost\r\n\r\n",
			numBytesPerRead: 3,
		}
		r, err := RequestFromReader(reader)
		require.Error(t, err)
		require.ErrorIs(t, err, headers.ErrMalformedFieldName)
		assert.Nil(t, r)
	})

	t.Run("header line missing colon propagates as an error", func(t *testing.T) {
		reader := &chunkReader{
			data:            "GET / HTTP/1.1\r\nNoColonHeader\r\n\r\n",
			numBytesPerRead: 3,
		}
		r, err := RequestFromReader(reader)
		require.Error(t, err)
		require.ErrorIs(t, err, headers.ErrMalformedFieldLine)
		assert.Nil(t, r)
	})

	t.Run("headers present but terminator missing is incomplete", func(t *testing.T) {
		// The stream ends after a valid header line but before the blank line
		// that ends the header block, so the request is truncated.
		reader := &chunkReader{
			data:            "GET / HTTP/1.1\r\nHost: localhost\r\n",
			numBytesPerRead: 3,
		}
		r, err := RequestFromReader(reader)
		require.ErrorIs(t, err, ErrIncompleteRequest)
		assert.Nil(t, r)
	})
}
