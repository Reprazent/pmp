// Package totp generates RFC 6238 time-based one-time passwords.
package totp

import (
	"crypto/hmac"
	"crypto/sha1" // #nosec G505 -- SHA-1 is the TOTP default (RFC 6238)
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrUnsupported is returned for TOTP formats this package doesn't handle
// (for example Steam Guard).
var ErrUnsupported = errors.New("unsupported TOTP format")

// Generate returns the code for secret at t. secret is either a base32 key
// or an otpauth://totp/ URI.
func Generate(secret string, t time.Time) (string, error) {
	key, digits, period, algo, err := parse(secret)
	if err != nil {
		return "", err
	}
	counter := uint64(t.Unix()) / uint64(period) // #nosec G115 -- times are after 1970
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(algo, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, code%mod), nil
}

func parse(s string) (key []byte, digits, period int, algo func() hash.Hash, err error) {
	digits, period, algo = 6, 30, sha1.New
	s = strings.TrimSpace(s)
	secret := s
	if strings.Contains(s, "://") {
		u, perr := url.Parse(s)
		if perr != nil || u.Scheme != "otpauth" || u.Host != "totp" {
			return nil, 0, 0, nil, ErrUnsupported
		}
		q := u.Query()
		secret = q.Get("secret")
		if d := q.Get("digits"); d != "" {
			if digits, err = strconv.Atoi(d); err != nil || digits < 1 || digits > 10 {
				return nil, 0, 0, nil, fmt.Errorf("invalid digits %q", d)
			}
		}
		if p := q.Get("period"); p != "" {
			if period, err = strconv.Atoi(p); err != nil || period < 1 {
				return nil, 0, 0, nil, fmt.Errorf("invalid period %q", p)
			}
		}
		switch strings.ToUpper(q.Get("algorithm")) {
		case "", "SHA1":
		case "SHA256":
			algo = sha256.New
		case "SHA512":
			algo = sha512.New
		default:
			return nil, 0, 0, nil, ErrUnsupported
		}
	}
	secret = strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(secret))
	secret = strings.TrimRight(secret, "=")
	key, err = base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(key) == 0 {
		return nil, 0, 0, nil, errors.New("invalid TOTP secret")
	}
	return key, digits, period, algo, nil
}
