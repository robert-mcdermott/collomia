package shell

import (
	"strings"
	"testing"
)

// publishes reports the analysis result for one command line.
func publishes(t *testing.T, command string) (bool, []string) {
	t.Helper()
	a := Analyze(command)
	return len(a.PublicationTargets) > 0, a.PublicationTargets
}

func TestPublicationIsClassifiedAcrossCategories(t *testing.T) {
	cases := []struct {
		command string
		label   string
	}{
		{"npm publish", publishRegistry},
		{"pnpm publish", publishRegistry},
		{"cargo publish", publishRegistry},
		{"gem push pkg-1.0.gem", publishRegistry},
		{"twine upload dist/*.whl", publishRegistry},
		{"poetry publish", publishRegistry},
		{"mvn deploy", publishRegistry},
		{"dotnet nuget push pkg.nupkg", publishRegistry},
		{"docker push registry.example.com/app:1", publishImage},
		{"podman push registry.example.com/app:1", publishImage},
		{"helm push chart.tgz oci://registry.example.com", publishImage},
		{"git push origin main", publishSourceRemote},
		{"git -C /work/repo push origin main", publishSourceRemote},
		{"gh pr create --fill", publishForge},
		{"gh release create v1.0.0", publishForge},
		{"gh issue close 4", publishForge},
		{"glab mr create", publishForge},
		{"gh api -X POST /repos/o/r/issues", publishForge},
		{"kubectl apply -f deploy.yaml", publishInfra},
		{"kubectl -n prod rollout restart deploy/web", publishInfra},
		{"helm upgrade --install app ./chart", publishInfra},
		{"terraform apply -auto-approve", publishInfra},
		{"terraform -chdir=infra apply", publishInfra},
		{"pulumi up", publishInfra},
		{"aws lambda update-function-code --function-name f --zip-file x.zip", publishInfra},
		{"aws --region us-west-2 ecs update-service --service x", publishInfra},
		{"aws s3 sync ./dist s3://bucket/", publishInfra},
		{"az group create --name x", publishInfra},
		{"gcloud run deploy svc --image i", publishInfra},
		{"ssh prod-server systemctl restart app", publishRemoteHost},
		{"scp ./build.tar prod-server:/srv/app/", publishRemoteHost},
		{"rsync -a ./dist/ prod-server:/var/www/", publishRemoteHost},
	}
	for _, tc := range cases {
		found, targets := publishes(t, tc.command)
		if !found {
			t.Errorf("%q was not classified as publication", tc.command)
			continue
		}
		if !strings.HasPrefix(targets[0], tc.label+": ") {
			t.Errorf("%q classified as %q, want label %q", tc.command, targets[0], tc.label)
		}
	}
}

func TestOrdinaryWorkIsNotPublication(t *testing.T) {
	// The control is only worth having if it stays quiet during the work a
	// coding agent does all day. Everything here reaches the network or a
	// deployment tool and still must not require a decision.
	ordinary := []string{
		"npm install", "npm install lodash", "npm run build", "npm test",
		"pip install requests", "go test ./...", "go mod tidy", "cargo build",
		"git status", "git fetch origin", "git pull", "git log --oneline",
		"docker build -t app .", "docker pull alpine",
		"kubectl get pods", "kubectl describe deploy web", "kubectl logs web-0",
		"terraform plan", "terraform init", "helm list", "helm template ./chart",
		"gh pr view 12", "gh pr list", "gh pr diff", "gh repo clone o/r",
		"gh api /repos/o/r", "gh auth status",
		"aws s3 ls", "aws ec2 describe-instances", "aws s3 sync s3://bucket/ ./dist",
		"gcloud compute instances list", "az account show",
		"ssh prod-server", "scp prod-server:/srv/log.txt ./",
		"rsync -a prod-server:/var/www/ ./dist/",
		"make build", "ls -la", "cat README.md",
	}
	for _, command := range ordinary {
		if found, targets := publishes(t, command); found {
			t.Errorf("%q was classified as publication (%v); ordinary work must not prompt", command, targets)
		}
	}
}

func TestRehearsalIsNotPublication(t *testing.T) {
	// A command that has asked the tool not to act must not be reported as if
	// it had. The inverse matters just as much: --dry-run=false is a request
	// to act and stays classified.
	for _, command := range []string{
		"npm publish --dry-run",
		"git push --dry-run origin main",
		"kubectl apply -f x.yaml --dry-run=client",
		"helm upgrade --install app ./chart --dry-run",
		"terraform apply --dry-run",
	} {
		if found, targets := publishes(t, command); found {
			t.Errorf("%q was classified as publication (%v)", command, targets)
		}
	}
	if found, _ := publishes(t, "kubectl apply -f x.yaml --dry-run=false"); !found {
		t.Error("--dry-run=false is a request to act and must stay classified")
	}
}

// TestGlobalOptionsDoNotHideTheOperation pins the two defects the first
// implementation shipped with. Both produced a plausible-looking operation
// string, so nothing failed loudly: the classifier simply stopped recognizing
// commands whenever an ordinary flag appeared.
func TestGlobalOptionsDoNotHideTheOperation(t *testing.T) {
	cases := map[string]string{
		// A namespace flag walked the verb reader past "apply" onto "prod".
		"kubectl -n prod apply -f x.yaml": "kubectl apply",
		// A flag's value was read as the verb: the operation became "aws lambda f".
		"aws lambda update-function-code --function-name f": "aws lambda update-function-code",
		// The method value became part of the operation: "gh api post".
		"gh api -X POST /repos/o/r/issues": "gh api",
		// A repository-directory flag must not become the subcommand.
		"git -C /work/repo push origin main": "git push",
		"helm -n prod upgrade app ./chart":   "helm upgrade",
		"docker -H tcp://x push a/b":         "docker push",
		"terraform -chdir=infra apply":       "terraform apply",
	}
	for command, want := range cases {
		a := Analyze(command)
		if len(a.Operations) == 0 {
			t.Errorf("%q produced no operation", command)
			continue
		}
		if a.Operations[0] != want {
			t.Errorf("%q operation = %q, want %q", command, a.Operations[0], want)
		}
	}
}

// TestPublicationSurvivesAnInlineInterpreter proves the merge path carries
// both fields. Without it `sh -c "npm publish"` would launder the operation
// the nested analysis already read.
func TestPublicationSurvivesAnInlineInterpreter(t *testing.T) {
	a := Analyze(`sh -c "npm publish"`)
	if len(a.PublicationTargets) == 0 {
		t.Fatal("an inline interpreter laundered the publication")
	}
	if len(a.Operations) == 0 || a.Operations[0] != "npm publish" {
		t.Fatalf("operations = %v, want npm publish", a.Operations)
	}
}

// TestDestructionAndPublicationAreClassifiedSymmetrically is the property this
// whole classifier exists for. Every tool below already required a fresh
// decision for its destructive verb; the publishing counterpart did not, even
// where it is the less reversible of the two. A new tool added to one side and
// not the other fails here.
func TestDestructionAndPublicationAreClassifiedSymmetrically(t *testing.T) {
	pairs := []struct{ destructive, publishing string }{
		{"terraform destroy -auto-approve", "terraform apply -auto-approve"},
		{"kubectl delete deployment web", "kubectl apply -f deploy.yaml"},
		{"helm uninstall app", "helm upgrade --install app ./chart"},
		{"pulumi destroy", "pulumi up"},
		{"git push --force origin main", "git push origin main"},
	}
	for _, pair := range pairs {
		destructive := Analyze(pair.destructive)
		if len(destructive.ConfirmReasons) == 0 {
			t.Errorf("%q no longer requires confirmation; the pairing below assumes it does", pair.destructive)
		}
		if found, _ := publishes(t, pair.publishing); !found {
			t.Errorf("%q is unclassified while its destructive counterpart %q requires confirmation", pair.publishing, pair.destructive)
		}
	}
}

// TestUnreadableOperationIsNotNamed keeps the analyzer honest in the same way
// the host extractor is: a word it could not read exactly is never recorded as
// if it were a verb.
func TestUnreadableOperationIsNotNamed(t *testing.T) {
	for _, command := range []string{"npm $VERB", "kubectl ${ACTION} -f x.yaml"} {
		a := Analyze(command)
		for _, operation := range a.Operations {
			if strings.ContainsAny(operation, "$*?") {
				t.Errorf("%q recorded an unreadable operation %q", command, operation)
			}
		}
	}
}
