package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"golang.org/x/crypto/argon2"
	"strings"
)

func Token() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func Digest(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }
func Password(p string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	h := argon2.IDKey([]byte(p), salt, 3, 64*1024, 2, 32)
	return base64.RawStdEncoding.EncodeToString(salt) + "." + base64.RawStdEncoding.EncodeToString(h)
}
func Check(encoded, p string) bool {
	x := strings.Split(encoded, ".")
	if len(x) != 2 {
		return false
	}
	s, e := base64.RawStdEncoding.DecodeString(x[0])
	if e != nil {
		return false
	}
	want, e := base64.RawStdEncoding.DecodeString(x[1])
	if e != nil {
		return false
	}
	got := argon2.IDKey([]byte(p), s, 3, 64*1024, 2, 32)
	return subtle.ConstantTimeCompare(want, got) == 1
}
