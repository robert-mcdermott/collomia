package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/credstore"
	"golang.org/x/term"
)

// runAuthCommand implements `collo auth`: optional storage of provider API
// keys in the operating system's credential manager.
//
// Nothing here logs into a Collomia service — there is none. The subcommand is
// local credential management, and every provider that works from an
// environment variable today keeps working untouched.
func runAuthCommand(opts options) error {
	if len(opts.args) == 0 {
		return errors.New("usage: collo auth [list|status|set|rm|import]")
	}
	switch opts.args[0] {
	case "list":
		return runAuthList()
	case "status":
		return runAuthStatus(opts)
	case "set":
		return runAuthSet(opts)
	case "rm", "remove", "delete":
		return runAuthRemove(opts)
	case "import":
		return runAuthImport(opts)
	default:
		return fmt.Errorf("unknown auth subcommand %q (expected list, status, set, rm, or import)", opts.args[0])
	}
}

func runAuthList() error {
	names, err := credstore.List()
	if err != nil {
		return err
	}
	if !credstore.Available() {
		fmt.Println("credential store: unavailable on this platform; providers read api_key, api_key_env, or their own environment variable")
		if len(names) == 0 {
			return nil
		}
		fmt.Println("(entries below were recorded on another platform and are not readable here)")
	} else {
		fmt.Println("credential store:", credstore.Backend())
	}
	if len(names) == 0 {
		fmt.Println("no stored credentials")
		return nil
	}
	for _, name := range names {
		state := "stored"
		if credstore.Available() {
			present, verifyErr := credstore.Verify(name)
			switch {
			case verifyErr != nil:
				state = "unreadable: " + verifyErr.Error()
			case !present:
				// The entry was removed outside Collomia. Saying so beats
				// listing a credential that will not resolve.
				state = "MISSING from " + credstore.Backend() + " — run `collo auth rm " + name + "` or store it again"
			}
		}
		fmt.Printf("  %-24s %s\n", name, state)
	}
	return nil
}

func runAuthStatus(opts options) error {
	cfg, err := appconfig.Load(opts.cwd)
	if err != nil {
		return err
	}
	only := ""
	if len(opts.args) > 1 {
		only = opts.args[1]
	}
	backend := credstore.Backend()
	if backend == "" {
		backend = "unavailable on this platform"
	}
	fmt.Println("credential store:", backend)
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	if only != "" {
		if _, ok := cfg.Providers[only]; !ok {
			return fmt.Errorf("no configured provider named %q", only)
		}
		names = []string{only}
	}
	if len(names) == 0 {
		fmt.Println("no providers configured")
		return nil
	}
	for _, name := range names {
		p := cfg.Providers[name]
		fmt.Printf("  %-24s %s\n", name, authSourceDescription(name, p))
	}
	fmt.Println()
	fmt.Println("Resolution order: api_key, then api_key_env, then the provider's own")
	fmt.Println("environment variable, then the credential store. An environment variable")
	fmt.Println("always wins over a stored credential.")
	return nil
}

// authSourceDescription explains, for one provider, where its credential came
// from — or why it needs none.
func authSourceDescription(name string, p appconfig.Provider) string {
	switch p.Auth {
	case "entra":
		return p.Type + "; Microsoft Entra (DefaultAzureCredential issues short-lived tokens; nothing to store)"
	case "sigv4":
		return p.Type + "; AWS SigV4 credential chain (nothing to store)"
	}
	if p.CredentialSource != "" {
		detail := p.Type + "; resolved from " + p.CredentialSource
		if p.CredentialSource == "credential store" {
			detail += " (" + credstore.Backend() + ")"
		}
		return detail
	}
	// A configured variable that is simply not exported is the most common
	// reason a provider has no credential, and naming it saves the user from
	// re-reading their own configuration to find out which one it was.
	if p.APIKeyEnv != "" {
		return fmt.Sprintf("%s; api_key_env %s is not set (export it, or run `collo auth set %s`)", p.Type, p.APIKeyEnv, name)
	}
	if env := appconfig.ImplicitCredentialEnv(p.Type); env != "" {
		return fmt.Sprintf("%s; no credential (set %s, api_key_env, or run `collo auth set %s`)", p.Type, env, name)
	}
	if !appconfig.StoredCredentialApplies(p) {
		return p.Type + "; no credential configured"
	}
	stored, _ := credstore.List()
	for _, candidate := range stored {
		if candidate == name {
			return p.Type + "; stored credential recorded but not readable — run `collo auth list`"
		}
	}
	return fmt.Sprintf("%s; no credential (fine for local endpoints; otherwise set api_key_env or run `collo auth set %s`)", p.Type, name)
}

func runAuthSet(opts options) error {
	if len(opts.args) < 2 {
		return errors.New("usage: collo auth set <provider>")
	}
	name := opts.args[1]
	if err := credstore.ValidateName(name); err != nil {
		return err
	}
	if !credstore.Available() {
		return unavailableError()
	}
	cfg, err := appconfig.Load(opts.cwd)
	if err != nil {
		return err
	}
	// A name that matches no provider is far more often a typo than a
	// credential stored ahead of its configuration, so it is worth saying —
	// but it is not an error, because writing the key first is legitimate.
	if p, ok := cfg.Providers[name]; !ok {
		fmt.Fprintf(os.Stderr, "note: no provider named %q is configured yet; the credential will be used once one is\n", name)
	} else if !appconfig.StoredCredentialApplies(p) {
		return fmt.Errorf("provider %q uses %s authentication, which takes no stored API key", name, p.Auth)
	}
	secret, err := readSecret(fmt.Sprintf("API key for %s: ", name))
	if err != nil {
		return err
	}
	if err := credstore.Set(name, secret); err != nil {
		return err
	}
	fmt.Printf("Stored the credential for %q in the %s.\n", name, credstore.Backend())
	if p, ok := cfg.Providers[name]; ok && p.CredentialSource != "" && p.CredentialSource != "credential store" {
		fmt.Printf("Note: %s still resolves from %s, which takes precedence. Unset it to use the stored credential.\n", name, p.CredentialSource)
	}
	return nil
}

func runAuthRemove(opts options) error {
	if len(opts.args) < 2 {
		return errors.New("usage: collo auth rm <provider>")
	}
	name := opts.args[1]
	if !credstore.Available() {
		return unavailableError()
	}
	existed, err := credstore.Delete(name)
	if err != nil {
		return err
	}
	if !existed {
		fmt.Printf("No stored credential for %q.\n", name)
		return nil
	}
	fmt.Printf("Removed the stored credential for %q.\n", name)
	return nil
}

// runAuthImport moves credentials that currently resolve from configuration or
// the environment into the credential store. It never overwrites an existing
// entry and never edits configuration files, so running it twice is safe and
// running it once cannot break a working setup.
func runAuthImport(opts options) error {
	if !credstore.Available() {
		return unavailableError()
	}
	cfg, err := appconfig.Load(opts.cwd)
	if err != nil {
		return err
	}
	stored, err := credstore.List()
	if err != nil {
		return err
	}
	requested := map[string]bool{}
	for _, name := range opts.args[1:] {
		requested[name] = true
	}
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		if len(requested) == 0 || requested[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	imported := 0
	for _, name := range names {
		p := cfg.Providers[name]
		switch {
		case p.APIKey == "" || !appconfig.StoredCredentialApplies(p):
			continue
		case p.CredentialSource == "credential store":
			continue
		case containsName(stored, name):
			fmt.Printf("  %-24s skipped: already stored (use `collo auth set %s` to replace it)\n", name, name)
			continue
		case credstore.ValidateName(name) != nil:
			fmt.Printf("  %-24s skipped: name cannot be stored\n", name)
			continue
		}
		if err := credstore.Set(name, p.APIKey); err != nil {
			return err
		}
		fmt.Printf("  %-24s imported from %s\n", name, p.CredentialSource)
		imported++
	}
	if imported == 0 {
		fmt.Println("Nothing to import: no provider resolves a credential that is not already stored.")
		return nil
	}
	fmt.Printf("\nImported %d credential(s) into the %s.\n", imported, credstore.Backend())
	fmt.Println("Configuration was not modified. The environment still takes precedence, so")
	fmt.Println("remove the api_key/api_key_env values or unset the variables to use the store.")
	return nil
}

func containsName(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// readSecret takes the credential without echoing it and without ever placing
// it in a command-line argument, where it would reach the process table and
// the shell history.
func readSecret(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		// A piped value supports scripted setup and is still never an argument.
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("no credential on standard input")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Print(prompt)
	data, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func unavailableError() error {
	return fmt.Errorf("%w: use api_key_env and export the variable instead", credstore.ErrUnsupported)
}
