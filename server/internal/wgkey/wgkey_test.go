package wgkey

import "testing"

func TestValidate(t *testing.T) {
	valid := "QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE="

	got, err := Validate(valid)
	if err != nil {
		t.Fatalf("Validate(valid) = %v", err)
	}
	if got != valid {
		t.Errorf("canonical form = %q, want %q", got, valid)
	}

	bad := map[string]string{
		"empty":         "",
		"too short":     "QUFB",
		"not base64":    "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!=",
		"31 bytes":      "eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eA==",
		"all zero key":  "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"shell attempt": "QUFB; rm -rf /; QUFBQUFBQUFBQUFBQUFBQUFBQUE=",
	}
	for name, key := range bad {
		if _, err := Validate(key); err == nil {
			t.Errorf("Validate(%s) accepted %q", name, key)
		}
	}
}
