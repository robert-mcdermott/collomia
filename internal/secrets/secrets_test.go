package secrets

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeHome points os.UserHomeDir at a temporary directory so the tables can be
// exercised without depending on the developer's own dotfiles.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	return home
}

func TestCredentialStoresAreRecognized(t *testing.T) {
	home := fakeHome(t)
	cases := map[string]string{
		filepath.Join(home, ".ssh", "id_rsa"):                        "SSH private key material",
		filepath.Join(home, ".ssh", "some_deploy_key"):               "SSH private key material",
		filepath.Join(home, ".aws", "credentials"):                   "AWS credentials",
		filepath.Join(home, ".config", "gcloud", "access_tokens.db"): "Google Cloud credentials",
		filepath.Join(home, ".kube", "config"):                       "Kubernetes cluster credentials",
		filepath.Join(home, ".docker", "config.json"):                "Docker registry authentication",
		filepath.Join(home, ".config", "gh", "hosts.yml"):            "GitHub CLI token",
		filepath.Join(home, ".gnupg", "secring.gpg"):                 "GnuPG keyring",
		filepath.Join(home, ".collomia", "config.json"):              "Collomia provider credentials",
		filepath.Join(home, ".git-credentials"):                      "stored Git credentials",
		filepath.Join("/srv", "app", ".env"):                         "environment file",
		filepath.Join("/srv", "app", ".env.production"):              "environment file",
		filepath.Join("/srv", "app", ".npmrc"):                       "npm registry token",
		filepath.Join("/srv", "app", "certs", "server.pem"):          "PEM key or certificate file",
		filepath.Join("/srv", "app", "certs", "tls.key"):             "private key file",
		filepath.Join("/srv", "app", "keystore.jks"):                 "Java keystore",
		filepath.Join("/opt", "deploy", "service-account.json"):      "service account key",
		filepath.Join("/var", "lib", "deploy", "id_ed25519"):         "SSH private key",
	}
	for path, want := range cases {
		if got := Classify(path); got != want {
			t.Errorf("Classify(%q) = %q, want %q", path, got, want)
		}
	}
}

// Everything here is routinely read, copied, or committed. Prompting on these
// would be the fastest way to teach a user to approve without looking.
func TestPublicAndOrdinaryFilesAreNotCredentials(t *testing.T) {
	home := fakeHome(t)
	for _, path := range []string{
		filepath.Join(home, ".ssh", "id_rsa.pub"),
		filepath.Join(home, ".ssh", "known_hosts"),
		filepath.Join(home, ".ssh", "known_hosts.old"),
		filepath.Join(home, ".ssh", "authorized_keys"),
		filepath.Join(home, ".ssh", "config"),
		filepath.Join("/srv", "app", ".env.example"),
		filepath.Join("/srv", "app", ".env.sample"),
		filepath.Join("/srv", "app", ".env.template"),
		filepath.Join("/srv", "app", "README.md"),
		filepath.Join("/srv", "app", "main.go"),
		filepath.Join("/srv", "app", "config.json"),
		filepath.Join("/srv", "app", "package.json"),
		// A bare "credentials" name is too common to flag on its own; the
		// stores that actually use it are matched by their home-anchored path.
		filepath.Join("/srv", "app", "credentials"),
	} {
		if got := Classify(path); got != "" {
			t.Errorf("Classify(%q) = %q, want no classification", path, got)
		}
	}
}

// A path that merely shares the home directory's textual prefix must not
// inherit the home-anchored rules.
//
// The sibling here is deliberately built by concatenation rather than by
// joining a "-backup" suffix: a suffixed sibling is rejected by anchor
// matching whether or not the separator is checked, so it proves nothing. This
// path reconstructs a valid anchor the instant the prefix is trimmed without
// requiring a separator, which is exactly the false positive being excluded.
func TestHomeMatchingRespectsPathSegments(t *testing.T) {
	home := fakeHome(t)
	// access_tokens.db classifies only via the home-anchored gcloud rule, so
	// it is a clean probe: no basename or extension rule can mask the result.
	inside := filepath.Join(home, ".config", "gcloud", "access_tokens.db")
	outside := home + filepath.ToSlash(filepath.Join(".config", "gcloud", "access_tokens.db"))
	if got := Classify(inside); got == "" {
		t.Fatalf("path inside home was not classified: %q", inside)
	}
	if got := Classify(outside); got != "" {
		t.Fatalf("Classify(%q) = %q, want no classification outside home", outside, got)
	}
}

func TestCaseVariantsStillClassify(t *testing.T) {
	home := fakeHome(t)
	for _, path := range []string{
		filepath.Join(home, ".AWS", "CREDENTIALS"),
		filepath.Join("/srv", "app", "Server.PEM"),
		filepath.Join("/srv", "app", ".Env"),
	} {
		if got := Classify(path); got == "" {
			t.Errorf("case variant was not classified: %q", path)
		}
	}
}

func TestClassifyArgumentResolvesTypedShorthands(t *testing.T) {
	fakeHome(t)
	cwd := filepath.Join("/srv", "app")
	cases := map[string]string{
		"~/.ssh/id_rsa":          "SSH private key material",
		"$HOME/.aws/credentials": "AWS credentials",
		"${HOME}/.npmrc":         "npm registry token",
		".env":                   "environment file",
		"./.env":                 "environment file",
		"certs/tls.key":          "private key file",
	}
	for token, want := range cases {
		if got := ClassifyArgument(token, cwd); got != want {
			t.Errorf("ClassifyArgument(%q) = %q, want %q", token, got, want)
		}
	}
}

func TestClassifyArgumentIgnoresFlagsAndEmptyTokens(t *testing.T) {
	fakeHome(t)
	for _, token := range []string{"-la", "--file", "", "   ", "-"} {
		if got := ClassifyArgument(token, "/srv/app"); got != "" {
			t.Errorf("ClassifyArgument(%q) = %q, want no classification", token, got)
		}
	}
}

func TestClassifyArgumentStripsSurroundingQuotes(t *testing.T) {
	fakeHome(t)
	if got := ClassifyArgument(`"~/.ssh/id_rsa"`, "/srv/app"); got == "" {
		t.Fatal("quoted path was not classified")
	}
}

// Documentation is generated against these lists, so they must be stable and
// free of duplicates.
func TestPublishedLocationListsAreSortedAndUnique(t *testing.T) {
	for name, list := range map[string][]string{"Locations": Locations(), "ExemptLocations": ExemptLocations()} {
		if len(list) == 0 {
			t.Fatalf("%s reported nothing", name)
		}
		for i := 1; i < len(list); i++ {
			if list[i-1] >= list[i] {
				t.Fatalf("%s is not sorted and unique: %q then %q", name, list[i-1], list[i])
			}
		}
	}
}

// Every location the package publishes must actually classify, or the
// documentation would promise protection that does not exist.
func TestEveryPublishedLocationClassifies(t *testing.T) {
	home := fakeHome(t)
	for _, location := range Locations() {
		probe := location
		switch {
		case strings.HasPrefix(probe, "~/"):
			probe = filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(probe, "~/")))
			if strings.HasSuffix(location, "/") {
				probe = filepath.Join(probe, "some_file")
			}
		case strings.HasPrefix(probe, "*"):
			probe = filepath.Join("/work", "repo", "material"+strings.TrimPrefix(probe, "*"))
		default:
			probe = filepath.Join("/work", "repo", probe)
		}
		if got := Classify(probe); got == "" {
			t.Errorf("published location %q did not classify (probe %q)", location, probe)
		}
	}
}
