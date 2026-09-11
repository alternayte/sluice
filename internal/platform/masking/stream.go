package masking

import (
	"bytes"
	"io"
)

// chunkSize is the read size of the stream masker.
const chunkSize = 32 << 10

// Reader returns a reader that masks the bytes of r as they stream (SI-01, SI-10).
// It keeps at most chunkSize plus maxForm-1 bytes in memory, so a form that spans two
// chunks is masked. The result is the same as Bytes on the full content.
// An empty masker returns r unchanged.
func (m *Masker) Reader(r io.Reader) io.Reader {
	if m.Empty() {
		return r
	}
	sr := &streamReader{m: m, src: r}
	for _, f := range m.forms {
		sr.first[f[0]] = append(sr.first[f[0]], []byte(f))
	}
	return sr
}

type streamReader struct {
	m   *Masker
	src io.Reader
	// first holds the forms by first byte, longest first, as the replacer orders them.
	first [256][][]byte
	buf   []byte // the read buffer
	in    []byte // input that is not examined yet
	out   []byte // masked output that is not read yet
	err   error  // the error of src, io.EOF at the end
}

func (s *streamReader) Read(p []byte) (int, error) {
	for len(s.out) == 0 {
		if s.err != nil {
			if len(s.in) > 0 {
				s.scan(true)
				continue
			}
			return 0, s.err
		}
		if s.buf == nil {
			s.buf = make([]byte, chunkSize)
		}
		n, err := s.src.Read(s.buf)
		s.in = append(s.in, s.buf[:n]...)
		s.err = err
		s.scan(false)
	}
	n := copy(p, s.out)
	s.out = s.out[n:]
	return n, nil
}

// scan masks the input up to the last position where a full form can start. Without final,
// it keeps the last maxForm-1 bytes, because a form can start there and end in the next chunk.
func (s *streamReader) scan(final bool) {
	end := len(s.in)
	if !final {
		end -= s.m.maxForm - 1
	}
	i, lit := 0, 0
	for i < end {
		matched := 0
		for _, f := range s.first[s.in[i]] {
			if bytes.HasPrefix(s.in[i:], f) {
				matched = len(f)
				break
			}
		}
		if matched == 0 {
			i++
			continue
		}
		s.out = append(s.out, s.in[lit:i]...)
		s.out = append(s.out, Mask...)
		i += matched
		lit = i
	}
	// A match can end after end, so i is the first byte that is not examined yet.
	s.out = append(s.out, s.in[lit:i]...)
	s.in = append(s.in[:0:0], s.in[i:]...)
}
