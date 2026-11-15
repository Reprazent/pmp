package envfile

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Formats supported by Write.
var Formats = []string{"sh", "dotenv", "json"}

// Write formats entries in the given format.
func Write(w io.Writer, format string, entries []Entry) error {
	var b strings.Builder
	switch format {
	case "sh", "":
		for _, e := range entries {
			fmt.Fprintf(&b, "export %s=%s\n", e.Key, ShQuote(e.Value))
		}
	case "dotenv":
		for _, e := range entries {
			fmt.Fprintf(&b, "%s=%s\n", e.Key, DotenvQuote(e.Value))
		}
	case "json":
		b.WriteString("{")
		for i, e := range entries {
			if i > 0 {
				b.WriteString(",")
			}
			k, _ := json.Marshal(e.Key)
			v, _ := json.Marshal(e.Value)
			fmt.Fprintf(&b, "\n  %s: %s", k, v)
		}
		if len(entries) > 0 {
			b.WriteString("\n")
		}
		b.WriteString("}\n")
	default:
		return fmt.Errorf("unknown format %q (want one of %s)", format, strings.Join(Formats, ", "))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// ShQuote quotes s for POSIX shells: single quotes, with ' written as '\”.
func ShQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// DotenvQuote double-quotes s using the escapes Parse understands.
func DotenvQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, `$`, `\$`)
	return `"` + r.Replace(s) + `"`
}
