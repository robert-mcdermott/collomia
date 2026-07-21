package main

import (
	"fmt"
	"io"
	"os"

	"github.com/robert-mcdermott/collomia/internal/event"
)

func runSchemaCommand(opts options) error {
	return writeEventSchema(os.Stdout, opts.args)
}

func writeEventSchema(out io.Writer, args []string) error {
	if len(args) != 1 || args[0] != "events" {
		return fmt.Errorf("schema requires exactly one contract: events")
	}
	_, err := out.Write(event.JSONSchema())
	return err
}
