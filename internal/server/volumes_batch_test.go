package server

import (
	"testing"

	"msb-manager/internal/api"
)

// planVolumeBatch is the pure reconcile seam: given the requested items and the
// set of volumes that already exist (name -> quota in MiB, from `msb volume ls`)
// it decides each item independently with NO subprocess calls. These table
// cases pin every branch of that decision.
func TestPlanVolumeBatch(t *testing.T) {
	existing := map[string]int{
		"v1": 1024, // 1G
		"v2": 2048, // 2G
	}

	cases := []struct {
		name       string
		req        []volumeRequest
		want       []api.VolumeResult
	}{
		{
			name: "absent volume is created",
			req:  []volumeRequest{{Name: "new", Size: "5G"}},
			want: []api.VolumeResult{{Name: "new", Size: "5G", Status: api.VolumeStatusCreated}},
		},
		{
			name: "present at matching size is exists",
			req:  []volumeRequest{{Name: "v1", Size: "1G"}},
			want: []api.VolumeResult{{Name: "v1", Size: "1G", Status: api.VolumeStatusExists}},
		},
		{
			name: "present at matching size in different units is exists",
			req:  []volumeRequest{{Name: "v1", Size: "1024M"}},
			want: []api.VolumeResult{{Name: "v1", Size: "1024M", Status: api.VolumeStatusExists}},
		},
		{
			name: "present at differing size is error",
			req:  []volumeRequest{{Name: "v1", Size: "5G"}},
			want: []api.VolumeResult{{
				Name: "v1", Size: "5G", Status: api.VolumeStatusError,
				Error: "exists at 1024MiB, cannot resize to 5120MiB",
			}},
		},
		{
			name: "mixed batch decides each item independently",
			req: []volumeRequest{
				{Name: "new", Size: "5G"},
				{Name: "v2", Size: "2G"},
				{Name: "v1", Size: "10G"},
			},
			want: []api.VolumeResult{
				{Name: "new", Size: "5G", Status: api.VolumeStatusCreated},
				{Name: "v2", Size: "2G", Status: api.VolumeStatusExists},
				{Name: "v1", Size: "10G", Status: api.VolumeStatusError, Error: "exists at 1024MiB, cannot resize to 10240MiB"},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := planVolumeBatch(c.req, existing)
			if len(got) != len(c.want) {
				t.Fatalf("got %d results, want %d: %+v", len(got), len(c.want), got)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("result[%d] = %+v, want %+v", i, got[i], c.want[i])
				}
			}
		})
	}
}
