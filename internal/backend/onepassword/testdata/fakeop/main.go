// Command fakeop pretends to be the op CLI for tests. It reads its state from
// files next to the binary:
//
//	secrets.json  {"op://...": "value"}
//	state         "signedin", "signedout" (no session but accounts), "none"
//	calls.log     appended: one line per call (args joined by spaces)
//	env.log       environment of the last call
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	args := os.Args[1:]

	f, _ := os.OpenFile(filepath.Join(dir, "calls.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	fmt.Fprintln(f, strings.Join(args, " "))
	f.Close()
	_ = os.WriteFile(filepath.Join(dir, "env.log"), []byte(strings.Join(os.Environ(), "\n")), 0o600)

	if len(args) >= 2 && args[0] == "--account" {
		args = args[2:]
	}
	state, _ := os.ReadFile(filepath.Join(dir, "state"))
	var secrets map[string]string
	raw, _ := os.ReadFile(filepath.Join(dir, "secrets.json"))
	_ = json.Unmarshal(raw, &secrets)

	signedIn := strings.TrimSpace(string(state)) == "signedin"
	notSignedIn := func() {
		fmt.Fprintln(os.Stderr, "[ERROR] 2026/01/01 00:00:00 You are not currently signed in. Please run `op signin --help` for instructions")
		os.Exit(1)
	}
	lookup := func(ref string) (string, bool) {
		if strings.Contains(ref, "Ambiguous") {
			fmt.Fprintf(os.Stderr, "[ERROR] 2026/01/01 00:00:00 More than one item matches %q.\n", ref)
			os.Exit(1)
		}
		v, ok := secrets[ref]
		return v, ok
	}

	switch args[0] {
	case "whoami":
		if !signedIn {
			fmt.Fprintln(os.Stderr, "[ERROR] 2026/01/01 00:00:00 account is not signed in")
			os.Exit(1)
		}
		fmt.Print(`{"url":"my.1password.com","email":"me@example.com"}`)
	case "account":
		if strings.TrimSpace(string(state)) == "none" {
			fmt.Print(`[]`)
			return
		}
		fmt.Print(`[{"url":"my.1password.com","email":"me@example.com"}]`)
	case "inject":
		if !signedIn {
			notSignedIn()
		}
		in, _ := io.ReadAll(os.Stdin)
		re := regexp.MustCompile(`\{\{\s*(.*?)\s*\}\}`)
		var missing string
		out := re.ReplaceAllStringFunc(string(in), func(m string) string {
			ref := re.FindStringSubmatch(m)[1]
			v, ok := lookup(ref)
			if !ok && missing == "" {
				missing = ref
			}
			return v
		})
		if missing != "" {
			fmt.Fprintf(os.Stderr, "[ERROR] 2026/01/01 00:00:00 could not resolve secret reference %q: could not find item\n", missing)
			os.Exit(1)
		}
		fmt.Println(out) // real op inject appends a newline
	case "read":
		if !signedIn {
			notSignedIn()
		}
		ref := args[len(args)-1]
		v, ok := lookup(ref)
		if !ok {
			fmt.Fprintf(os.Stderr, "[ERROR] 2026/01/01 00:00:00 could not read secret %q: \"Nope\" isn't an item in the \"Private\" vault.\n", ref)
			os.Exit(1)
		}
		fmt.Print(v)
	default:
		fmt.Fprintln(os.Stderr, "unknown command")
		os.Exit(2)
	}
}
