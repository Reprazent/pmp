// Command pmp reads secrets from several password managers.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/Reprazent/pmp/internal/cli"
	"github.com/Reprazent/pmp/internal/exitcode"
)

// Set by -ldflags at release time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	opts := &cli.Options{Registry: registry()}
	root := cli.NewRootCommand(cli.BuildInfo{Version: version, Commit: commit, Date: date}, opts)
	err := cli.Execute(root)
	if err != nil {
		var exit *exitcode.ExitError
		if !errors.As(err, &exit) {
			fmt.Fprintln(os.Stderr, "pmp: "+err.Error())
		}
	}
	os.Exit(exitcode.For(err))
}
