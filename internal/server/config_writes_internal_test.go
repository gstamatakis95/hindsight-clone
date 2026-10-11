package server

import (
	"memd/internal/ai"
	"memd/internal/config"
	"memd/internal/domain"
	"reflect"
	"testing"
)

// TestServerDefaultsStillValidate covers Accept 5: the server defaults (every LLM slot bound, plus
// RERANK) pass config.RPCBankConfig, and written whole as a bank or tenant level through the validating
// patch they are accepted and stored unchanged.
func TestServerDefaultsStillValidate(t *testing.T) {
	env := map[string]string{ai.EnvModel: "m", ai.EnvModelPrefix + "RERANK": "r"}
	defaults, err := config.ServerDefaults(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := config.ValidateOverrides(config.RPCBankConfig, defaults); err != nil {
		t.Fatalf("server defaults: %v", err)
	}
	d := Deps{ValidateOverrides: config.ValidateOverrides}
	patch, err := configPatch(configValueToProto(defaults), nil, d.checker(config.RPCBankConfig))
	if err != nil {
		t.Fatal(err)
	}
	got, err := patch(domain.BankConfig{})
	if err != nil {
		t.Fatalf("defaults written as a level: %v", err)
	}
	if !reflect.DeepEqual(got, defaults) {
		t.Fatalf("written defaults differ:\n got  %+v\n want %+v", got, defaults)
	}
}
