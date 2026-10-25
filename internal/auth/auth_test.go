package auth

import "testing"

func TestPassword(t *testing.T) {
	h := Password("long password")
	if !Check(h, "long password") || Check(h, "wrong") {
		t.Fatal("password verification")
	}
	if len(Token()) < 40 {
		t.Fatal("token too short")
	}
}
