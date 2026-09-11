package masking

import (
	"bytes"
	"encoding/base64"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

// readAll reads r to the end.
func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReaderMasksFormsAcrossChunks(t *testing.T) {
	secret := `p@ss/w0rd+"x"?&=`
	m := New([]string{secret})
	b64 := base64.StdEncoding.EncodeToString([]byte(secret))
	// Put forms at each offset around the chunk boundary.
	for _, form := range []string{secret, b64} {
		for off := chunkSize - len(form) - 1; off <= chunkSize+1; off++ {
			in := strings.Repeat("a", off) + form + "tail" + form
			want := strings.Repeat("a", off) + Mask + "tail" + Mask
			for name, r := range map[string]io.Reader{
				"chunks":   strings.NewReader(in),
				"one byte": iotest.OneByteReader(strings.NewReader(in)),
			} {
				if got := readAll(t, m.Reader(r)); got != want {
					t.Fatalf("%s, offset %d: form not masked", name, off)
				}
			}
		}
	}
}

func TestReaderMatchesBytes(t *testing.T) {
	m := New([]string{"secret-one", "secret", "abcd"})
	in := strings.Repeat("xx secret-one secretabcd abc secre", 5000) + "secret"
	want := string(m.Bytes([]byte(in)))
	if got := readAll(t, m.Reader(iotest.HalfReader(strings.NewReader(in)))); got != want {
		t.Fatal("stream result differs from Bytes")
	}
	if strings.Contains(want, "secret") || strings.Contains(want, "abcd") {
		t.Fatal("a value is left in the output")
	}
}

func TestReaderWithoutSecretValues(t *testing.T) {
	m := New([]string{"secret"})
	in := bytes.Repeat([]byte("no values here\n"), 10000)
	if got := readAll(t, m.Reader(bytes.NewReader(in))); got != string(in) {
		t.Fatal("text without values changed")
	}
	if got := readAll(t, m.Reader(strings.NewReader(""))); got != "" {
		t.Fatalf("empty input gave %q", got)
	}
}

func TestReaderEmptyMaskerPassthrough(t *testing.T) {
	r := strings.NewReader("secret")
	if New(nil).Reader(r) != io.Reader(r) {
		t.Fatal("empty masker wrapped the reader")
	}
	var nilMasker *Masker
	if got := readAll(t, nilMasker.Reader(strings.NewReader("abcd"))); got != "abcd" {
		t.Fatalf("nil masker gave %q", got)
	}
}

func TestReaderReturnsSourceError(t *testing.T) {
	boom := io.ErrUnexpectedEOF
	r := New([]string{"secret"}).Reader(io.MultiReader(strings.NewReader("a secret"), iotest.ErrReader(boom)))
	b, err := io.ReadAll(r)
	if err != boom || string(b) != "a ***" {
		t.Fatalf("got %q %v", b, err)
	}
}
