package headers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHeadersParse drives the parser through a table of inputs covering the
// happy path, streaming/partial buffers, and every malformed-input branch.
//
// Each case asserts the full contract: how many bytes were consumed (n),
// whether the header block is finished (done), which sentinel error (if any)
// was returned, and the resulting header values.
func TestHeadersParse(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantN       int
		wantDone    bool
		wantErr     error             // nil means "expect success"
		wantHeaders map[string]string // verified via Get (keys are case-insensitive)
	}{
		// ---- happy path -------------------------------------------------
		{
			name:        "single header followed by terminator",
			input:       "Host: localhost:42069\r\n\r\n",
			wantN:       25,
			wantDone:    true,
			wantHeaders: map[string]string{"Host": "localhost:42069"},
		},
		{
			name:     "multiple headers in one call",
			input:    "Host: localhost:42069\r\nUser-Agent: curl/8.0\r\nAccept: */*\r\n\r\n",
			wantN:    60,
			wantDone: true,
			wantHeaders: map[string]string{
				"Host":       "localhost:42069",
				"User-Agent": "curl/8.0",
				"Accept":     "*/*",
			},
		},
		{
			name:        "surrounding spaces in value are trimmed",
			input:       "FooFoo:     barbar\r\n\r\n",
			wantN:       22,
			wantDone:    true,
			wantHeaders: map[string]string{"FooFoo": "barbar"},
		},
		{
			name:        "surrounding tabs in value are trimmed",
			input:       "X-Tab:\t\tbar\t\t\r\n\r\n",
			wantN:       17,
			wantDone:    true,
			wantHeaders: map[string]string{"X-Tab": "bar"},
		},
		{
			name:        "empty value is allowed",
			input:       "X-Empty:\r\n\r\n",
			wantN:       12,
			wantDone:    true,
			wantHeaders: map[string]string{"X-Empty": ""},
		},
		{
			name:        "colons inside the value are preserved",
			input:       "X-Time: 12:30:45\r\n\r\n",
			wantN:       20,
			wantDone:    true,
			wantHeaders: map[string]string{"X-Time": "12:30:45"},
		},
		{
			name:        "internal spaces in value are preserved",
			input:       "X-Msg: hello world foo\r\n\r\n",
			wantN:       26,
			wantDone:    true,
			wantHeaders: map[string]string{"X-Msg": "hello world foo"},
		},
		{
			name:        "field name with all valid special tchars",
			input:       "X-A_b.c!: val\r\n\r\n",
			wantN:       17,
			wantDone:    true,
			wantHeaders: map[string]string{"X-A_b.c!": "val"},
		},
		{
			name:     "only the terminating CRLF, no headers",
			input:    "\r\n",
			wantN:    2,
			wantDone: true,
		},

		// ---- streaming / partial buffers --------------------------------
		{
			// A complete line was consumed and stored, but without the
			// trailing empty line the block is not finished yet.
			name:        "complete line but block not terminated",
			input:       "Host: localhost\r\n",
			wantN:       17,
			wantDone:    false,
			wantHeaders: map[string]string{"Host": "localhost"},
		},
		{
			name:     "partial line with no CRLF yet",
			input:    "Host: localho",
			wantN:    0,
			wantDone: false,
		},
		{
			name:     "empty input",
			input:    "",
			wantN:    0,
			wantDone: false,
		},

		// ---- malformed input (all return n=0, done=false) ---------------
		{
			name:    "leading whitespace before field name",
			input:   "       Host: localhost:42069\r\n\r\n",
			wantErr: ErrMalformedFieldName,
		},
		{
			name:    "trailing space in field name",
			input:   "Host : localhost\r\n\r\n",
			wantErr: ErrMalformedFieldName,
		},
		{
			name:    "missing colon",
			input:   "NoColonHeader\r\n\r\n",
			wantErr: ErrMalformedFieldLine,
		},
		{
			name:    "invalid character in field name",
			input:   "H@st: localhost\r\n\r\n",
			wantErr: ErrMalformedFieldName,
		},
		{
			name:    "space inside field name",
			input:   "Foo Bar: baz\r\n\r\n",
			wantErr: ErrMalformedFieldName,
		},
		{
			// RFC 7230: token = 1*tchar, so an empty name is invalid.
			name:    "empty field name",
			input:   ": value\r\n\r\n",
			wantErr: ErrMalformedFieldName,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHeaders()
			n, done, err := h.Parse([]byte(tt.input))

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				// On error the parser reports no progress.
				assert.Equal(t, 0, n)
				assert.False(t, done)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantN, n, "bytes consumed")
			assert.Equal(t, tt.wantDone, done, "done flag")
			for k, v := range tt.wantHeaders {
				assert.Equal(t, v, h.Get(k), "Get(%q)", k)
			}
		})
	}
}

// TestHeadersGetSetCaseInsensitive documents that field names are stored and
// retrieved case-insensitively, and that a missing key yields the zero value.
func TestHeadersGetSetCaseInsensitive(t *testing.T) {
	h := NewHeaders()
	h.Set("Content-Type", "application/json")

	assert.Equal(t, "application/json", h.Get("Content-Type"))
	assert.Equal(t, "application/json", h.Get("content-type"))
	assert.Equal(t, "application/json", h.Get("CONTENT-TYPE"))
	assert.Equal(t, "", h.Get("Missing-Header"))

	// A second Set on the same key (any casing) folds rather than overwrites;
	// the dedicated folding contract lives in TestHeadersSetMultiValue.
	h.Set("CONTENT-TYPE", "text/html")
	assert.Equal(t, "application/json, text/html", h.Get("Content-Type"))

	// The same case-insensitivity holds for parsed headers.
	parsed := NewHeaders()
	_, _, err := parsed.Parse([]byte("Host: example.com\r\n\r\n"))
	require.NoError(t, err)
	assert.Equal(t, "example.com", parsed.Get("HOST"))
}

// TestHeadersIncrementalParsing simulates the real-world use of Parse: a caller
// accumulates bytes from the network and calls Parse repeatedly until done.
// Each call consumes the complete header lines available so far and reports how
// many bytes it used, so the caller can slice them off its buffer.
func TestHeadersIncrementalParsing(t *testing.T) {
	full := []byte("Host: localhost:42069\r\nUser-Agent: curl/8.0\r\nAccept: */*\r\n\r\n")

	h := NewHeaders()
	buf := []byte{}
	pos := 0 // how many bytes we have "received" from the wire so far
	done := false

	// Feed one byte at a time. The +10 bound is a guard so a buggy parser
	// that never reports done can't hang the test.
	for i := 0; !done && i < len(full)+10; i++ {
		if pos < len(full) {
			buf = append(buf, full[pos])
			pos++
		}

		n, d, err := h.Parse(buf)
		require.NoError(t, err)

		buf = buf[n:] // drop the bytes the parser already consumed
		done = d
	}

	require.True(t, done, "parser should reach the end of the header block")
	assert.Empty(t, buf, "all consumed bytes should have been sliced away")
	assert.Equal(t, len(full), pos, "every byte should have been fed in")
	assert.Equal(t, "localhost:42069", h.Get("Host"))
	assert.Equal(t, "curl/8.0", h.Get("User-Agent"))
	assert.Equal(t, "*/*", h.Get("Accept"))
}

// TestHeadersMultiValueParse verifies RFC 7230 §3.2.2 value folding: repeated
// field names are combined into a single comma-separated value, in arrival
// order, rather than overwriting one another. "Foo: a\r\nFoo: b" is defined by
// the spec to be equivalent to "Foo: a, b".
func TestHeadersMultiValueParse(t *testing.T) {
	t.Run("two occurrences fold in order", func(t *testing.T) {
		h := NewHeaders()
		data := []byte("Set-Person: lane\r\nSet-Person: prime\r\n\r\n")

		n, done, err := h.Parse(data)
		require.NoError(t, err)
		assert.True(t, done)
		assert.Equal(t, len(data), n)
		assert.Equal(t, "lane, prime", h.Get("Set-Person"))
	})

	t.Run("three occurrences fold in order", func(t *testing.T) {
		h := NewHeaders()
		data := []byte("Accept: text/html\r\nAccept: application/json\r\nAccept: */*\r\n\r\n")

		_, _, err := h.Parse(data)
		require.NoError(t, err)
		assert.Equal(t, "text/html, application/json, */*", h.Get("Accept"))
	})

	t.Run("folding is case-insensitive on the field name", func(t *testing.T) {
		h := NewHeaders()
		// Different casings of the same field name must fold together, since
		// keys are normalized to lower case before lookup.
		data := []byte("X-Tag: a\r\nx-tag: b\r\nX-TAG: c\r\n\r\n")

		_, _, err := h.Parse(data)
		require.NoError(t, err)
		assert.Equal(t, "a, b, c", h.Get("X-Tag"))
	})

	t.Run("distinct field names are not folded together", func(t *testing.T) {
		h := NewHeaders()
		data := []byte("Host: a\r\nAccept: b\r\nHost: c\r\n\r\n")

		_, _, err := h.Parse(data)
		require.NoError(t, err)
		assert.Equal(t, "a, c", h.Get("Host"))
		assert.Equal(t, "b", h.Get("Accept"))
	})

	t.Run("a repeated header with an empty value still inserts a separator", func(t *testing.T) {
		h := NewHeaders()
		// Folding concatenates unconditionally, so an empty second value
		// yields a trailing ", " — current behavior, pinned intentionally.
		data := []byte("X-Empty: a\r\nX-Empty:\r\n\r\n")

		_, _, err := h.Parse(data)
		require.NoError(t, err)
		assert.Equal(t, "a, ", h.Get("X-Empty"))
	})
}

// TestHeadersSetMultiValue exercises Set directly (not via Parse) to document
// its folding contract as a unit:
//   - the first Set stores the value verbatim;
//   - each subsequent Set appends ", value";
//   - the key is matched case-insensitively across calls.
func TestHeadersSetMultiValue(t *testing.T) {
	h := NewHeaders()

	h.Set("Cache-Control", "max-age=3600")
	assert.Equal(t, "max-age=3600", h.Get("Cache-Control"), "first Set stores verbatim")

	h.Set("Cache-Control", "must-revalidate")
	assert.Equal(t, "max-age=3600, must-revalidate", h.Get("Cache-Control"))

	// Different casing targets the same stored entry and keeps folding.
	h.Set("cache-control", "no-store")
	assert.Equal(t, "max-age=3600, must-revalidate, no-store", h.Get("Cache-Control"))
}
