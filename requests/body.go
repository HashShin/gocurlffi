package requests

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// readBody reads the whole body, invoking cb for every raw chunk, then decodes
// any Content-Encoding the server applied.
func readBody(r io.Reader, headers *Headers, cb func([]byte) error, maxRecvSpeed int) ([]byte, error) {
	var raw bytes.Buffer
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if cb != nil {
				if cerr := cb(chunk); cerr != nil {
					return nil, cerr
				}
			}
			raw.Write(chunk)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return decodeContentEncoding(raw.Bytes(), headers)
}

func decodeContentEncoding(data []byte, headers *Headers) ([]byte, error) {
	enc := headers.Get("Content-Encoding")
	if enc == "" || len(data) == 0 {
		return data, nil
	}
	parts := strings.Split(enc, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.ToLower(strings.TrimSpace(parts[i]))
		switch part {
		case "", "identity":
			continue
		case "gzip", "x-gzip":
			zr, err := gzip.NewReader(bytes.NewReader(data))
			if err != nil {
				return data, nil
			}
			out, err := io.ReadAll(zr)
			if err != nil {
				return data, nil
			}
			data = out
		case "deflate":
			out, ok := inflate(data)
			if ok {
				data = out
			}
		case "br", "brotli":
			out, err := io.ReadAll(brotli.NewReader(bytes.NewReader(data)))
			if err == nil {
				data = out
			}
		case "zstd", "zstandard":
			zr, err := zstd.NewReader(bytes.NewReader(data))
			if err != nil {
				continue
			}
			out, err := io.ReadAll(zr)
			zr.Close()
			if err == nil {
				data = out
			}
		}
	}
	return data, nil
}

func inflate(data []byte) ([]byte, bool) {
	if zr, err := zlib.NewReader(bytes.NewReader(data)); err == nil {
		if out, err := io.ReadAll(zr); err == nil {
			return out, true
		}
	}
	if fr := flate.NewReader(bytes.NewReader(data)); fr != nil {
		if out, err := io.ReadAll(fr); err == nil {
			return out, true
		}
	}
	return data, false
}

// decodingReader wraps a body reader and decompresses it on the fly.
func decodingReader(r io.Reader, headers *Headers) io.ReadCloser {
	enc := strings.ToLower(strings.TrimSpace(headers.Get("Content-Encoding")))
	switch enc {
	case "gzip", "x-gzip":
		if zr, err := gzip.NewReader(r); err == nil {
			return zr
		}
	case "br", "brotli":
		return io.NopCloser(brotli.NewReader(r))
	case "zstd", "zstandard":
		if zr, err := zstd.NewReader(r); err == nil {
			return &closingReader{zr, func() error {
				zr.Close()
				if closer, ok := r.(io.Closer); ok {
					return closer.Close()
				}
				return nil
			}}
		}
	case "deflate":
		if zr, err := zlib.NewReader(r); err == nil {
			return &closingReader{zr, func() error {
				_ = zr.Close()
				if closer, ok := r.(io.Closer); ok {
					return closer.Close()
				}
				return nil
			}}
		}
	}
	return io.NopCloser(r)
}

// closingReader pairs a decompressing reader with the underlying body closer.
type closingReader struct {
	io.Reader
	closeFn func() error
}

func (c *closingReader) Close() error { return c.closeFn() }

// cancelReadCloser releases the per-request context when a streaming body is
// closed, so the timeout does not abort an in-progress body read.
type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelReadCloser) Close() error {
	err := c.ReadCloser.Close()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	return err
}
