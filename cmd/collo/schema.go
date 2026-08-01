package main

import (
	"fmt"
	"io"
	"os"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/event"
)

func runSchemaCommand(opts options) error {
	return writeSchema(os.Stdout, opts.args)
}

// schemaContracts are the published contracts this build can print.
var schemaContracts = []string{"events", "config"}

func writeSchema(out io.Writer, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("schema requires exactly one contract: %s", englishList(schemaContracts))
	}
	switch args[0] {
	case "events":
		_, err := out.Write(event.JSONSchema())
		return err
	case "config":
		_, err := out.Write(appconfig.JSONSchema())
		return err
	default:
		return fmt.Errorf("unknown contract %q: %s", args[0], englishList(schemaContracts))
	}
}

func englishList(values []string) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		return values[0]
	case 2:
		return values[0] + " or " + values[1]
	default:
		last := len(values) - 1
		joined := ""
		for _, value := range values[:last] {
			joined += value + ", "
		}
		return joined + "or " + values[last]
	}
}
