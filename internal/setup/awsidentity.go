package setup

import (
	"context"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

// identityTimeout bounds the STS call. It is a diagnostic, so it must never be
// the reason setup feels slow or hangs; a machine whose credential chain is
// itself stuck (an expired SSO session waiting on a browser) is exactly the
// case this must not block on.
const identityTimeout = 10 * time.Second

// AWSIdentity is who the AWS credential chain actually resolved to.
type AWSIdentity struct {
	Account string
	ARN     string
	Source  string
	Err     error
}

// Describe renders the identity as one line for the confirmation screen.
func (i AWSIdentity) Describe() string {
	if i.Err != nil {
		return "could not be determined: " + i.Err.Error()
	}
	if i.ARN == "" {
		return "unknown"
	}
	detail := i.ARN
	if i.Account != "" {
		detail += " (account " + i.Account + ")"
	}
	return detail
}

// ResolveAWSIdentity reports which identity the credential chain produced.
//
// Bedrock's commonest confusion is not that a credential is missing but that
// six possible sources exist — environment variables, a named profile, SSO, an
// assumed role, instance identity — and nothing says which one won. A
// permission error against a model then reads as a Bedrock problem when it is
// really the wrong account. The Bedrock adapter loads its configuration exactly
// this way, so what this reports is what the session will use.
//
// It is strictly diagnostic: a failure here never blocks setup, because an
// account without sts:GetCallerIdentity can still invoke a model it has been
// granted.
func ResolveAWSIdentity(ctx context.Context, p appconfig.Provider) AWSIdentity {
	identity := AWSIdentity{}
	callCtx, cancel := context.WithTimeout(ctx, identityTimeout)
	defer cancel()

	options := []func(*awsconfig.LoadOptions) error{}
	if region := strings.TrimSpace(p.Region); region != "" {
		options = append(options, awsconfig.WithRegion(region))
	}
	if profile := strings.TrimSpace(p.Profile); profile != "" {
		options = append(options, awsconfig.WithSharedConfigProfile(profile))
		identity.Source = "profile " + profile
	} else {
		identity.Source = "default credential chain"
	}

	cfg, err := awsconfig.LoadDefaultConfig(callCtx, options...)
	if err != nil {
		identity.Err = err
		return identity
	}
	out, err := sts.NewFromConfig(cfg).GetCallerIdentity(callCtx, &sts.GetCallerIdentityInput{})
	if err != nil {
		identity.Err = err
		return identity
	}
	if out.Account != nil {
		identity.Account = *out.Account
	}
	if out.Arn != nil {
		identity.ARN = *out.Arn
	}
	return identity
}
