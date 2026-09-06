package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// CommandVerification is model-selected scope, never proof. The Standard
// runtime binds an observed exit status to unchanged, authorized file bytes.
type CommandVerification struct {
	Paths   []string `json:"paths"`
	Purpose string   `json:"purpose"`
}

func ParseCommandVerification(raw json.RawMessage) (*CommandVerification, error) {
	var args struct {
		Verification json.RawMessage `json:"verification"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if len(args.Verification) == 0 || string(args.Verification) == "null" {
		return nil, nil
	}
	v := &CommandVerification{}
	decoder := json.NewDecoder(bytes.NewReader(args.Verification))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return nil, err
	}
	if len(v.Paths) == 0 || len(v.Paths) > 16 || strings.TrimSpace(v.Purpose) == "" || len(v.Purpose) > 512 {
		return nil, errors.New("verification requires 1–16 file or project-directory paths and a purpose of 1–512 bytes")
	}
	for _, p := range v.Paths {
		if strings.TrimSpace(p) == "" || len(p) > 4096 {
			return nil, errors.New("verification paths must contain 1–4096 bytes")
		}
	}
	return v, nil
}
