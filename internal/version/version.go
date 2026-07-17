package version

import "fmt"

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func String() string { return fmt.Sprintf("collo %s (%s, %s)", Version, Commit, Date) }
