package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/robert-mcdermott/collomia/internal/supportbundle"
	"github.com/robert-mcdermott/collomia/internal/userconfig"
	"github.com/robert-mcdermott/collomia/internal/version"
)

func runSupportCommand(opts options) error {
	if len(opts.args) != 1 || opts.args[0] != "bundle" {
		return fmt.Errorf("support requires `bundle` (usage: collo support bundle [--output path] [--include-logs])")
	}
	output := opts.output
	if output == "" {
		dir, err := userconfig.Path("support")
		if err != nil {
			return err
		}
		output = supportbundle.DefaultPath(dir, time.Now())
	} else if !filepath.IsAbs(output) {
		output = filepath.Join(opts.cwd, output)
	}
	result, err := supportbundle.Create(supportbundle.Options{
		Workspace: opts.cwd, Output: output, Version: version.String(),
		IncludeLogs: opts.includeLogs, Capabilities: capabilityMarkdown(),
	})
	if err != nil {
		return err
	}
	fmt.Println("Created", result.Path)
	fmt.Println("Bundle ID:", result.ID)
	if result.LogFiles > 0 {
		fmt.Printf("Included %d recent redacted log file(s) by explicit request.\n", result.LogFiles)
	} else {
		fmt.Println("Debug logs were not included.")
	}
	fmt.Println("Review the archive before sharing it; redaction is defense in depth.")
	return nil
}
