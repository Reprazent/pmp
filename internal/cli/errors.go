package cli

import (
	"fmt"

	"github.com/Reprazent/pmp/internal/exitcode"
)

func usageErr(err error) error { return exitcode.NewUsage(err) }

func usagef(format string, args ...any) error {
	return exitcode.NewUsage(fmt.Errorf(format, args...))
}
