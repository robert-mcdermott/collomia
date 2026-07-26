package version

import (
	"fmt"
	"strings"
)

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func String() string { return fmt.Sprintf("collo %s (%s, %s)", Version, Commit, Date) }

// Short is the name and the version alone — the identity a header needs,
// without the build detail only a bug report needs.
func Short() string { return "collo " + Version }

// Build is the commit and the calendar day it was built. The time of day and
// the zone offset are dropped: they tell a reader nothing the day does not,
// and they were more than half the length of the line they sat on.
func Build() string {
	day := Date
	if i := strings.IndexByte(day, 'T'); i > 0 {
		day = day[:i]
	}
	return Commit + " · " + day
}
