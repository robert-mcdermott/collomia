package sandbox

import (
	"encoding/base64"
	"encoding/json"
)

// EncodePolicy serializes a policy for a platform re-exec shim. The encoding
// is an implementation detail and never contains credentials.
func EncodePolicy(policy Policy) (string, error) {
	data, err := json.Marshal(policy)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// DecodePolicy reverses EncodePolicy.
func DecodePolicy(encoded string) (Policy, error) {
	var policy Policy
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return policy, err
	}
	err = json.Unmarshal(data, &policy)
	return policy, err
}
