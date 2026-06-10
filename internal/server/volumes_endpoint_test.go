package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"msb-manager/internal/api"
	"msb-manager/internal/msb"
)

func postVolumes(t *testing.T, client MsbClient, body string) *httptest.ResponseRecorder {
	t.Helper()
	srv := New(Config{Token: testToken}, client)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authedJSON(http.MethodPost, "/volumes", body))
	return rec
}

func decodeResults(t *testing.T, rec *httptest.ResponseRecorder) []api.VolumeResult {
	t.Helper()
	var got api.VolumeBatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode results: %v; body=%s", err, rec.Body.String())
	}
	return got.Results
}

// Back-compat guard: the original single {name,size} shape must still return
// 201 with the original {name,size} body and shell out to VolumeCreate once.
func TestPostVolumes_SingleShapeUnchanged(t *testing.T) {
	client := &fakeMsb{}
	rec := postVolumes(t, client, `{"name":"data","size":"1G"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != `{"name":"data","size":"1G"}`+"\n" && got != `{"name":"data","size":"1G"}` {
		t.Errorf("body = %q, want single {name,size}", got)
	}
	if client.gotVolumeCreate != [2]string{"data", "1G"} {
		t.Errorf("VolumeCreate got %v, want [data 1G]", client.gotVolumeCreate)
	}
}

func TestPostVolumes_BatchAllCreatedOrExists_Returns201(t *testing.T) {
	client := &fakeMsb{volumeListOut: []msb.Volume{{Name: "v1", QuotaMiB: 1024}}}
	rec := postVolumes(t, client, `{"volumes":[{"name":"new","size":"5G"},{"name":"v1","size":"1G"}]}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	results := decodeResults(t, rec)
	want := []api.VolumeResult{
		{Name: "new", Size: "5G", Status: api.VolumeStatusCreated},
		{Name: "v1", Size: "1G", Status: api.VolumeStatusExists},
	}
	if len(results) != 2 || results[0] != want[0] || results[1] != want[1] {
		t.Errorf("results = %+v, want %+v", results, want)
	}
	// Only the `created` item shells out.
	if len(client.volumeCreateNames) != 1 || client.volumeCreateNames[0] != "new" {
		t.Errorf("VolumeCreate names = %v, want [new]", client.volumeCreateNames)
	}
}

func TestPostVolumes_BatchWithMismatch_Returns207(t *testing.T) {
	client := &fakeMsb{volumeListOut: []msb.Volume{{Name: "v1", QuotaMiB: 1024}}}
	rec := postVolumes(t, client, `{"volumes":[{"name":"new","size":"5G"},{"name":"v1","size":"10G"}]}`)

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207; body=%s", rec.Code, rec.Body.String())
	}
	results := decodeResults(t, rec)
	if len(results) != 2 || results[0].Status != api.VolumeStatusCreated || results[1].Status != api.VolumeStatusError {
		t.Errorf("results = %+v, want [created, error]", results)
	}
	if results[1].Error == "" {
		t.Error("mismatch result should carry an error message")
	}
	if len(client.volumeCreateNames) != 1 || client.volumeCreateNames[0] != "new" {
		t.Errorf("VolumeCreate names = %v, want [new] (error item must not shell out)", client.volumeCreateNames)
	}
}

func TestPostVolumes_PreflightFailureMakesZeroMsbCalls(t *testing.T) {
	cases := []string{
		`{"volumes":[]}`,                                       // empty list
		`{"volumes":[{"name":"--force","size":"1G"}]}`,        // flag-shaped name
		`{"volumes":[{"name":"ok","size":"-1G"}]}`,            // bad size
		`{"volumes":[{"name":"ok","size":"1G"},{"size":"2G"}]}`, // second item missing name
	}
	for _, body := range cases {
		client := &fakeMsb{}
		rec := postVolumes(t, client, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rec.Code)
		}
		if client.volumeCreateCalls != 0 {
			t.Errorf("body %s: VolumeCreate called %d times, want 0", body, client.volumeCreateCalls)
		}
		if client.volumeListCalls != 0 {
			t.Errorf("body %s: VolumeList called %d times, want 0 (pre-flight is side-effect-free)", body, client.volumeListCalls)
		}
	}
}

func TestPostVolumes_BatchCreateFailure_Returns207(t *testing.T) {
	client := &fakeMsb{volumeCreateErr: errors.New("boom")}
	rec := postVolumes(t, client, `{"volumes":[{"name":"new","size":"5G"}]}`)

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207; body=%s", rec.Code, rec.Body.String())
	}
	results := decodeResults(t, rec)
	if len(results) != 1 || results[0].Status != api.VolumeStatusError {
		t.Errorf("results = %+v, want one error", results)
	}
	if client.volumeCreateCalls != 1 {
		t.Errorf("VolumeCreate called %d times, want 1", client.volumeCreateCalls)
	}
}
