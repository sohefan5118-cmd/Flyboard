package secretbox

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"golang.org/x/crypto/chacha20poly1305"
	"os"
	"strings"
)

type Box struct {
	key []byte
	ID  string
}

func Load(path string) (*Box, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	s := strings.TrimSpace(string(b))
	raw, e := base64.RawStdEncoding.DecodeString(s)
	if e != nil {
		raw, e = base64.StdEncoding.DecodeString(s)
	}
	if e != nil || len(raw) != chacha20poly1305.KeySize {
		return nil, errors.New("master key must be base64-encoded 32 bytes")
	}
	return &Box{raw, "v1"}, nil
}
func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("key must be 32 bytes")
	}
	return &Box{append([]byte(nil), key...), "v1"}, nil
}
func (b *Box) Seal(plain []byte, aad string) ([]byte, error) {
	a, e := chacha20poly1305.NewX(b.key)
	if e != nil {
		return nil, e
	}
	n := make([]byte, a.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return nil, e
	}
	out := append([]byte(b.ID+":"), n...)
	return a.Seal(out, n, plain, []byte(aad)), nil
}
func (b *Box) Open(in []byte, aad string) ([]byte, error) {
	p := strings.IndexByte(string(in), ':')
	if p < 0 || string(in[:p]) != b.ID {
		return nil, errors.New("unknown key id")
	}
	a, _ := chacha20poly1305.NewX(b.key)
	if len(in) < p+1+a.NonceSize() {
		return nil, errors.New("invalid envelope")
	}
	n := in[p+1 : p+1+a.NonceSize()]
	out, e := a.Open(nil, n, in[p+1+a.NonceSize():], []byte(aad))
	if e != nil {
		return nil, fmt.Errorf("decrypt: %w", e)
	}
	return out, nil
}
