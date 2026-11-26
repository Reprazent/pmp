package cache

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const dump = `keychain: "/Users/me/Library/Keychains/login.keychain-db"
version: 512
class: "genp"
attributes:
    "acct"<blob>="aaaa"
    "svce"<blob>="pmp"
keychain: "/Users/me/Library/Keychains/login.keychain-db"
version: 512
class: "genp"
attributes:
    "acct"<blob>="bbbb"
    "svce"<blob>="something-else"
keychain: "/Users/me/Library/Keychains/login.keychain-db"
version: 512
class: "inet"
attributes:
    "acct"<blob>="cccc"
    "svce"<blob>="pmp"
keychain: "/Users/me/Library/Keychains/login.keychain-db"
version: 512
class: "genp"
attributes:
    "acct"<blob>=<NULL>
    "svce"<blob>="pmp"
keychain: "/Users/me/Library/Keychains/login.keychain-db"
version: 512
class: "genp"
attributes:
    "svce"<blob>="pmp"
    "acct"<blob>="dddd"
`

func TestAccounts(t *testing.T) {
	got := accounts([]byte(dump), "pmp")
	if want := []string{"aaaa", "dddd"}; !reflect.DeepEqual(got, want) {
		t.Errorf("accounts = %v, want %v", got, want)
	}
}

// TestKeychain writes to the real login Keychain under a test service, so
// it only runs with PMP_KEYCHAIN_TEST=1.
func TestKeychain(t *testing.T) {
	if os.Getenv("PMP_KEYCHAIN_TEST") != "1" {
		t.Skip("set PMP_KEYCHAIN_TEST=1 to test against the login Keychain")
	}
	k := &Keychain{Service: "pmp-test"}
	t.Cleanup(func() { _ = k.Clear(ctx) })
	_ = k.Clear(ctx)

	exp := time.Unix(time.Now().Add(time.Hour).Unix(), 0)
	in := Entry{Ref: "op://Private/Test/field", Value: "s3cr3t\nwith 'quotes'", Expires: exp}
	if err := k.Set(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, ok, err := k.Get(ctx, in.Ref)
	if err != nil || !ok || got.Value != in.Value || !got.Expires.Equal(exp) {
		t.Fatalf("Get = %+v, %v, %v", got, ok, err)
	}
	if _, ok, _ := k.Get(ctx, "op://Private/Test/other"); ok {
		t.Error("Get of a missing ref hit")
	}
	if err := k.Set(ctx, Entry{Ref: "bw://Test/password", Value: "x", Expires: exp}); err != nil {
		t.Fatal(err)
	}
	es, err := k.List(ctx)
	if err != nil || len(es) != 2 || es[0].Ref != "bw://Test/password" {
		t.Fatalf("List = %v, %v", es, err)
	}
	if ok, err := k.Delete(ctx, in.Ref); !ok || err != nil {
		t.Errorf("Delete = %v, %v", ok, err)
	}
	if err := k.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if es, _ := k.List(ctx); len(es) != 0 {
		t.Errorf("after Clear: %v", es)
	}
}

// fakeSecurity points securityPath at a script for the rest of the test.
func fakeSecurity(t *testing.T, script string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "security")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := securityPath
	securityPath = path
	t.Cleanup(func() { securityPath = old })
}

// A security process that hangs (say, behind an unlock dialog) is killed
// when the context is done, for every operation.
func TestKeychainHonoursContext(t *testing.T) {
	fakeSecurity(t, "exec sleep 30")
	k := &Keychain{Service: "pmp-test"}
	ops := map[string]func(context.Context) error{
		"Get":    func(ctx context.Context) error { _, _, err := k.Get(ctx, "op://a/b/c"); return err },
		"Set":    func(ctx context.Context) error { return k.Set(ctx, Entry{Ref: "op://a/b/c", Value: "v"}) },
		"Delete": func(ctx context.Context) error { _, err := k.Delete(ctx, "op://a/b/c"); return err },
		"List":   func(ctx context.Context) error { _, err := k.List(ctx); return err },
		"Clear":  func(ctx context.Context) error { return k.Clear(ctx) },
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			start := time.Now()
			err := op(ctx)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("err = %v, want DeadlineExceeded", err)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("took %s, want the hung process killed", d)
			}
		})
	}
}

func TestKeychainFake(t *testing.T) {
	// Answers find-generic-password with an entry for op://a/b/c and
	// fails everything else as not found, like security does.
	data, _ := encode(Entry{Ref: "op://a/b/c", Value: "v", Expires: time.Unix(1_800_000_000, 0)})
	fakeSecurity(t, `case "$1" in
find-generic-password) [ "$5" = "`+Key("op://a/b/c")+`" ] && echo '`+base64Prefix+base64.StdEncoding.EncodeToString([]byte(data))+`' && exit 0;;
-i) cat > "$(dirname "$0")/stdin"; exit 0;;
esac
echo "security: The specified item could not be found in the keychain." >&2; exit 44`)
	k := &Keychain{Service: "pmp-test"}
	ctx := context.Background()

	e, ok, err := k.Get(ctx, "op://a/b/c")
	if err != nil || !ok || e.Value != "v" {
		t.Errorf("Get = %+v, %v, %v", e, ok, err)
	}
	if _, ok, err := k.Get(ctx, "op://a/b/missing"); ok || err != nil {
		t.Errorf("Get missing = %v, %v", ok, err)
	}
	if ok, err := k.Delete(ctx, "op://a/b/c"); ok || err != nil {
		t.Errorf("Delete missing = %v, %v", ok, err)
	}
	if err := k.Set(ctx, Entry{Ref: "op://a/b/c", Value: "secret"}); err != nil {
		t.Fatal(err)
	}
	stdin, _ := os.ReadFile(filepath.Join(filepath.Dir(securityPath), "stdin"))
	if !strings.HasPrefix(string(stdin), "add-generic-password -U -s 'pmp-test' -a '"+Key("op://a/b/c")+"' -w '"+base64Prefix) || strings.Contains(string(stdin), "secret") {
		t.Errorf("security -i got %q", stdin)
	}
	if err := k.Set(ctx, Entry{Ref: "op://a/b/c", Value: strings.Repeat("x", 4096)}); !errors.Is(err, ErrTooBig) {
		t.Errorf("Set big = %v, want ErrTooBig", err)
	}
}
