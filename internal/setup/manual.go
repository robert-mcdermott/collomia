package setup

import (
	"strings"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

// FieldKind distinguishes a typed value from one chosen off a fixed list.
type FieldKind int

const (
	// FieldText is free text.
	FieldText FieldKind = iota
	// FieldChoice cycles through Options.
	FieldChoice
	// FieldSecret is free text that is never echoed.
	FieldSecret
)

// Field is one input on a manual provider form.
type Field struct {
	Key         string
	Label       string
	Hint        string
	Kind        FieldKind
	Options     []string
	Placeholder string
	Default     string
	// Optional fields may be left blank.
	Optional bool
}

// Manual is a provider that cannot be configured from a name and a key, and so
// is described by its fields rather than offered as a one-line choice.
//
// Azure and Bedrock are here because neither is discoverable in the way a local
// runtime is. Azure addresses deployments inside a resource you name, and
// Bedrock resolves an identity through the AWS credential chain and grants
// model access per region. Presenting either as a single list entry would
// produce a choice that dead-ends one screen later.
type Manual struct {
	Name string
	Key  string
	Type string
	// Summary is the one-line description on the provider list.
	Summary string
	// Detail explains what the user is about to need, shown above the form,
	// because the commonest failure here is not knowing which of several
	// similar Azure values a field wants.
	Detail string
	Fields []Field
	// Discovers reports whether the provider publishes a model catalog, which
	// decides whether the model is chosen or typed.
	Discovers bool
}

const (
	azureAuthHint = "api-key uses a key from the resource. entra uses DefaultAzureCredential and refreshes automatically. bearer is a token you supply and Collomia cannot refresh."
	// Named in full, because "SigV4" is the one term on any of these screens
	// that assumes knowledge a first-time user may not have. It is AWS's
	// request-signing scheme, and choosing it means Collomia asks for no key
	// here — the AWS SDK finds one the way every other AWS tool does.
	bedrockAuthStr = "sigv4 signs with ordinary IAM credentials — exported AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY (plus AWS_SESSION_TOKEN if temporary), a named profile, SSO, or an instance role. No key is entered here. bearer uses a Bedrock API key, which you paste. auto picks bearer when a token is present and sigv4 otherwise."
)

// ManualCandidates returns every provider configured through a form.
func ManualCandidates() []Manual {
	return []Manual{
		{
			Name: "Azure OpenAI", Key: "azure-openai", Type: "azure-openai",
			Summary: "deployment-scoped endpoint in an Azure OpenAI resource",
			Detail:  "Azure OpenAI addresses a deployment you created, not a published model name. Take the endpoint and deployment from the resource in the Azure portal.",
			Fields: []Field{
				{Key: "base_url", Label: "Resource endpoint", Hint: "the resource's endpoint, without a path", Placeholder: "https://my-resource.openai.azure.com"},
				{Key: "deployment", Label: "Deployment name", Hint: "the name you gave the deployment, not the model's published name", Placeholder: "my-gpt4o-deployment"},
				{Key: "api_version", Label: "API version", Hint: "leave as-is unless the portal shows another", Default: "2024-10-21"},
				{Key: "auth", Label: "Authentication", Kind: FieldChoice, Options: []string{"api-key", "entra", "bearer"}, Hint: azureAuthHint},
			},
		},
		{
			Name: "Azure AI Foundry", Key: "azure-foundry", Type: "azure-foundry",
			Summary: "Foundry project speaking the OpenAI v1 route",
			Detail:  "Foundry publishes a model catalog, so the model is chosen rather than typed once the endpoint answers.",
			Fields: []Field{
				{Key: "base_url", Label: "Project endpoint", Hint: "Collomia appends /openai/v1 itself", Placeholder: "https://my-project.services.ai.azure.com"},
				{Key: "auth", Label: "Authentication", Kind: FieldChoice, Options: []string{"api-key", "entra", "bearer"}, Hint: azureAuthHint},
			},
			Discovers: true,
		},
		{
			Name: "Azure AI Foundry (Anthropic)", Key: "azure-foundry-anthropic", Type: "azure-foundry-anthropic",
			Summary: "Foundry deployment speaking the Anthropic Messages route",
			Detail:  "For Anthropic models hosted in Foundry. Collomia appends /anthropic to the endpoint itself.",
			Fields: []Field{
				{Key: "base_url", Label: "Project endpoint", Hint: "without the /anthropic suffix", Placeholder: "https://my-project.services.ai.azure.com"},
				{Key: "auth", Label: "Authentication", Kind: FieldChoice, Options: []string{"api-key", "entra", "bearer"}, Hint: azureAuthHint},
			},
		},
		{
			Name: "AWS Bedrock", Key: "bedrock", Type: "bedrock",
			Summary: "ConverseStream through the AWS credential chain",
			Detail:  "Bedrock publishes no model catalog through this route, so the model id is typed. Model access is granted per region in the Bedrock console.",
			Fields: []Field{
				{Key: "region", Label: "Region", Hint: "the region where you were granted model access", Placeholder: "us-west-2"},
				{Key: "auth", Label: "Authentication", Kind: FieldChoice, Options: []string{"auto", "sigv4", "bearer"}, Hint: bedrockAuthStr},
				{Key: "profile", Label: "AWS profile", Hint: "leave blank for the default credential chain", Optional: true, Placeholder: "default"},
			},
		},
		{
			Name: "Something else", Key: "custom", Type: "openai-compatible",
			Summary: "any OpenAI-compatible endpoint — you supply the URL",
			Detail:  "Anything that speaks the OpenAI Chat Completions API: a gateway, a self-hosted server, or a provider not listed above.",
			Fields: []Field{
				{Key: "name", Label: "Name", Hint: "how this provider is referred to in configuration", Default: "custom"},
				{Key: "base_url", Label: "Base URL", Hint: "the API root, including any /v1 the provider expects", Placeholder: "http://host:port/v1"},
			},
			Discovers: true,
		},
	}
}

// CredentialEnvVars returns every environment variable the wizard consults
// before asking for a key, paired with the provider it applies to.
//
// It exists so the documentation cannot drift from the code: a guard test
// requires each name here to appear in the user guide. A variable the wizard
// silently honors but nobody documents is a feature only its author can use.
func CredentialEnvVars() map[string]string {
	vars := map[string]string{}
	for _, hosted := range HostedCandidates() {
		vars[hosted.EnvVar] = hosted.Name
		if implicit := appconfig.ImplicitCredentialEnv(hosted.Type); implicit != "" {
			vars[implicit] = hosted.Name
		}
	}
	for _, manual := range ManualCandidates() {
		if name := ManualEnvVar(manual); name != "" {
			vars[name] = manual.Name
		}
	}
	return vars
}

// ManualEnvVar is the variable a form-configured provider reads a key from,
// and the name recorded as `api_key_env` when the key is stored as an
// environment reference.
func ManualEnvVar(spec Manual) string {
	switch spec.Type {
	case "bedrock":
		return appconfig.ImplicitCredentialEnv("bedrock")
	case "azure-openai", "azure-foundry", "azure-foundry-anthropic":
		return "AZURE_OPENAI_API_KEY"
	case "openai-compatible":
		// The custom path has no conventional variable, because the endpoint
		// behind it is unknown. Its key is stored or named explicitly instead.
		return ""
	}
	return strings.ToUpper(strings.ReplaceAll(spec.Key, "-", "_")) + "_API_KEY"
}

// NeedsCredential reports whether a completed form still requires a key.
//
// Entra and the SigV4 chain deliberately have nothing to store: Entra issues
// short-lived tokens through DefaultAzureCredential, and SigV4 draws on the AWS
// chain, which already owns profiles, SSO, roles, and instance identity. Asking
// for a key in either case would invite a user to paste one that is then never
// consulted.
func (m Manual) NeedsCredential(values map[string]string) bool {
	switch strings.TrimSpace(values["auth"]) {
	case "entra", "sigv4":
		return false
	case "auto":
		// Bedrock's auto resolves bearer only when a token is present; the
		// wizard offers to supply one, and falling back to the chain is fine.
		return m.Type == "bedrock"
	}
	return m.Type != "openai-compatible" || strings.TrimSpace(values["base_url"]) != ""
}

// Build assembles a provider from completed form values.
func (m Manual) Build(values map[string]string) (name string, p appconfig.Provider) {
	name = m.Key
	if custom := strings.TrimSpace(values["name"]); custom != "" {
		name = custom
	}
	p = appconfig.Provider{
		Type:       m.Type,
		BaseURL:    strings.TrimRight(strings.TrimSpace(values["base_url"]), "/"),
		Region:     strings.TrimSpace(values["region"]),
		Profile:    strings.TrimSpace(values["profile"]),
		Deployment: strings.TrimSpace(values["deployment"]),
		APIVersion: strings.TrimSpace(values["api_version"]),
	}
	// "api-key" and "auto" are the adapters' implicit behavior, so they are not
	// written out. A configuration file that states a default it would have
	// taken anyway is noise the reader has to check against the reference.
	if auth := strings.TrimSpace(values["auth"]); auth != "" && auth != "api-key" && auth != "auto" {
		p.Auth = auth
	}
	return name, p
}

// Validate reports the first field a form is missing, so submission can refuse
// with a specific message instead of failing later inside an adapter.
func (m Manual) Validate(values map[string]string) string {
	for _, field := range m.Fields {
		if field.Optional || field.Kind == FieldChoice {
			continue
		}
		if strings.TrimSpace(values[field.Key]) == "" {
			return field.Label + " is required"
		}
	}
	if strings.TrimSpace(values["auth"]) == "entra" && m.Type == "bedrock" {
		return "entra is not an AWS authentication mode"
	}
	return ""
}
