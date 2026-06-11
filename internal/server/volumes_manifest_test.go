package server

import (
	"strings"
	"testing"
)

func TestParseVolumeManifest_YAMLAndJSON(t *testing.T) {
	yamlBody := "volumes:\n  - name: alpine-data\n    size: 1G\n  - name: pg-data\n    size: 10G\n"
	jsonBody := `{"volumes":[{"name":"alpine-data","size":"1G"},{"name":"pg-data","size":"10G"}]}`

	for _, body := range []string{yamlBody, jsonBody} {
		m, err := parseVolumeManifest([]byte(body))
		if err != nil {
			t.Fatalf("parse %q: %v", body, err)
		}
		if len(m.Volumes) != 2 ||
			m.Volumes[0].Name != "alpine-data" || m.Volumes[0].Size != "1G" ||
			m.Volumes[1].Name != "pg-data" || m.Volumes[1].Size != "10G" {
			t.Errorf("parse %q = %+v, want two items", body, m.Volumes)
		}
	}
}

func TestParseVolumeManifest_RejectsUnknownFields(t *testing.T) {
	body := `{"volumes":[{"name":"a","size":"1G","bogus":true}]}`
	if _, err := parseVolumeManifest([]byte(body)); err == nil {
		t.Error("expected error for unknown field, got nil")
	}
}

func TestVolumeManifest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"valid", `{"volumes":[{"name":"a","size":"1G"}]}`, false},
		{"empty list", `{"volumes":[]}`, true},
		{"missing list", `{}`, true},
		{"missing name", `{"volumes":[{"size":"1G"}]}`, true},
		{"missing size", `{"volumes":[{"name":"a"}]}`, true},
		{"invalid name (flag-shaped)", `{"volumes":[{"name":"--force","size":"1G"}]}`, true},
		{"invalid size", `{"volumes":[{"name":"a","size":"-1G"}]}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := parseVolumeManifest([]byte(c.body))
			if err != nil {
				// A parse failure is also a rejection; only acceptable when we expect one.
				if !c.wantErr {
					t.Fatalf("unexpected parse error: %v", err)
				}
				return
			}
			err = m.validate()
			if c.wantErr && err == nil {
				t.Errorf("validate(%s) = nil, want error", c.body)
			}
			if !c.wantErr && err != nil {
				t.Errorf("validate(%s) = %v, want nil", c.body, err)
			}
		})
	}
}

func TestVolumeManifest_EmptyListMessageIsClear(t *testing.T) {
	m, err := parseVolumeManifest([]byte(`{"volumes":[]}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	err = m.validate()
	if err == nil || !strings.Contains(err.Error(), "volumes") {
		t.Errorf("error = %v, want a message mentioning the empty volumes list", err)
	}
}
