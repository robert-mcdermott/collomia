package config

import "testing"

func TestStandardTotalLimitDefaultsAndValidation(t *testing.T) {
	for _, value := range []int{0, 1, 500, 10000} {
		cfg := validConfig()
		cfg.Options.MaxTurnIterations = value
		if errs := cfg.ValidateFields(); len(errs) != 0 {
			t.Fatalf("valid limit %d: %v", value, errs)
		}
	}
	for _, value := range []int{-1, 10001} {
		cfg := validConfig()
		cfg.Options.MaxTurnIterations = value
		if errs := cfg.ValidateFields(); len(errs) == 0 {
			t.Fatalf("invalid limit %d accepted", value)
		}
	}
	if Defaults().Options.MaxTurnIterations != 256 {
		t.Fatal("wrong default")
	}
}
