package totp

import (
	"encoding/base32"
	"errors"
	"testing"
	"time"
)

// Test vectors from RFC 6238 appendix B.
func TestRFC6238(t *testing.T) {
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	sha1Key := enc.EncodeToString([]byte("12345678901234567890"))
	sha256Key := enc.EncodeToString([]byte("12345678901234567890123456789012"))
	sha512Key := enc.EncodeToString([]byte("1234567890123456789012345678901234567890123456789012345678901234"))

	tests := []struct {
		unix   int64
		secret string
		want   string
	}{
		{59, "otpauth://totp/x?digits=8&secret=" + sha1Key, "94287082"},
		{59, "otpauth://totp/x?digits=8&algorithm=SHA256&secret=" + sha256Key, "46119246"},
		{59, "otpauth://totp/x?digits=8&algorithm=SHA512&secret=" + sha512Key, "90693936"},
		{1111111109, "otpauth://totp/x?digits=8&secret=" + sha1Key, "07081804"},
		{20000000000, "otpauth://totp/x?digits=8&secret=" + sha1Key, "65353130"},
		{59, sha1Key, "287082"},
		{59, "  " + sha1Key[:4] + " " + sha1Key[4:], "287082"},
	}
	for _, tt := range tests {
		got, err := Generate(tt.secret, time.Unix(tt.unix, 0))
		if err != nil {
			t.Fatalf("Generate(%q): %v", tt.secret, err)
		}
		if got != tt.want {
			t.Errorf("Generate(%q, %d) = %s, want %s", tt.secret, tt.unix, got, tt.want)
		}
	}
}

func TestErrors(t *testing.T) {
	if _, err := Generate("steam://ABC", time.Now()); !errors.Is(err, ErrUnsupported) {
		t.Errorf("steam: %v", err)
	}
	if _, err := Generate("not base32 !", time.Now()); err == nil {
		t.Error("expected error for bad secret")
	}
	if _, err := Generate("", time.Now()); err == nil {
		t.Error("expected error for empty secret")
	}
}
