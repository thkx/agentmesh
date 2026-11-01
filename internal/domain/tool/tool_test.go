package tool

import "testing"

func TestSpecValidate(t *testing.T) {
	s := Spec{Name: "echo", Description: "echo", Permission: PermissionRead, Input: Schema{Type: "object", Properties: map[string]Property{"value": {Type: "string"}}, Required: []string{"value"}}}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.Input.Required = []string{"missing"}
	if err := s.Validate(); err == nil {
		t.Fatal("expected invalid required property")
	}
}
