package engine

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestUUIDTemplateFunctions(t *testing.T) {
	dns := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	for name, want := range map[string]string{"uuid.v3": "5df41881-3aed-3515-88a7-2f4a814cf09e", "uuid.v5": "2ed6657d-e927-568b-95e1-2665a8aea6a2"} {
		got, err := templateFunction(name, map[string]string{"name": "www.example.com", "namespace": dns})
		if err != nil || got != want {
			t.Errorf("%s = %q %v, want %q", name, got, err, want)
		}
	}
	if _, err := templateFunction("uuid.v5", map[string]string{"name": "x", "namespace": "nope"}); err == nil {
		t.Fatal("invalid namespace accepted")
	}
	for name, version := range map[string]byte{"uuid.v1": '1', "uuid.v6": '6'} {
		got, err := templateFunction(name, map[string]string{})
		if err != nil || len(got) != 36 || got[14] != version || !strings.ContainsAny(got[19:20], "89ab") {
			t.Errorf("%s = %q %v", name, got, err)
		}
	}
	// A v6 UUID sorts by its time, which is its leading hex digits.
	at := time.Date(2025, 5, 28, 11, 15, 0, 0, time.UTC)
	early, _ := templateFunction("uuid.v6", map[string]string{"timestamp": "2025-05-28T11:15:00Z"})
	late := timeUUID(6, at.Add(time.Second))
	if early[:13] >= late[:13] || timeUUID(6, at)[:13] != early[:13] {
		t.Fatal(early, late)
	}
}

func TestOnePasswordArguments(t *testing.T) {
	ctx := context.Background()
	for values, want := range map[[2]string]string{{"token", ""}: "Missing service token", {"desktop", ""}: "Missing account name", {"other", "x"}: "Invalid authentication method"} {
		if _, err := onePasswordValue(ctx, map[string]string{"authMethod": values[0], "token": values[1]}); err == nil || err.Error() != want {
			t.Errorf("%v: %v, want %q", values, err, want)
		}
	}
	d := onePasswordDefinition()
	label, _, secret, visible := d.Fields[1].Describe(map[string]string{"authMethod": "desktop"})
	if label != "Account Name" || secret || !visible {
		t.Fatal(label, secret, visible)
	}
	if label, _, secret, _ = d.Fields[1].Describe(map[string]string{"authMethod": "token"}); label != "Token" || !secret {
		t.Fatal(label, secret)
	}
}
