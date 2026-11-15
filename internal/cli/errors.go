package cli

import "github.com/Reprazent/pmp/internal/exitcode"

func usageErr(err error) error { return exitcode.NewUsage(err) }
