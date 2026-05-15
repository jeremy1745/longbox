package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/service"
)

// --- Minimal stubs ---

// stubAcqSvc is a stand-in for *service.AcquisitionService for handler tests.
// AcquisitionService is a concrete struct, so we can't mock it via interface
// in the handler without refactoring. Instead, we test the handler logic that
// doesn't require a real AcquisitionService by wiring a helper that calls
// WantAndTrackSeries through a thin interface defined just for these tests.
//
// The handler uses *service.AcquisitionService directly. To exercise the
// handler code paths we care about (validation, conflict passthrough, success)
// without a full DB, we skip the handler construction and test the request
// parsing / response encoding logic via a lightweight adapter approach.
//
// We can test WantTrack by calling it as a plain http.HandlerFunc via httptest;
// we just need a way to inject the stub service. Since AcquisitionHandler holds
// a *service.AcquisitionService (concrete), the cleanest approach for these
// unit tests is to verify the 400 (validation) path — which fires before any
// service call — and the conflict / success paths via integration-level tests
// in acquisition_test.go. We document this decision below.

// TestWantTrack_MissingIDs verifies that a request with neither comicvine_id
// nor metron_id is rejected with 400 MISSING_ID before any service call.
// This exercises the handler's own validation logic, which is the most
// important thing to cover in a pure handler unit test.
func TestWantTrack_MissingIDs(t *testing.T) {
	// Construct a handler with nil acqSvc — the 400 fires before it's called.
	h := &AcquisitionHandler{acqSvc: nil, wantListRepo: nil}

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/pull-list/want-track", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.WantTrack(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("response has no 'error' object: %v", resp)
	}
	if errObj["code"] != "MISSING_ID" {
		t.Errorf("error code: got %q, want MISSING_ID", errObj["code"])
	}
}

// TestWantTrack_InvalidBody verifies that malformed JSON produces a 400.
func TestWantTrack_InvalidBody(t *testing.T) {
	h := &AcquisitionHandler{acqSvc: nil, wantListRepo: nil}

	req := httptest.NewRequest(http.MethodPost, "/pull-list/want-track", bytes.NewBufferString("not json"))
	rr := httptest.NewRecorder()

	h.WantTrack(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

// TestWantTrack_ConflictPassthrough verifies that writeMatchConflict correctly
// converts a *SeriesMatchConflictError into a 409 MERGE_REQUIRED response.
// This tests the package-level writeMatchConflict helper (which IS reachable
// without a real AcquisitionService) by calling it directly, mirroring how the
// handler uses it.
func TestWantTrack_ConflictPassthrough(t *testing.T) {
	conflicting := &model.Series{ID: 42, Title: "X-Men"}
	conflict := &service.SeriesMatchConflictError{
		RequestedSeriesID: 0,
		ConflictingSeries: conflicting,
	}

	w := httptest.NewRecorder()
	handled := writeMatchConflict(w, 0, conflict)
	if !handled {
		t.Fatal("writeMatchConflict returned false for a *SeriesMatchConflictError")
	}
	if w.Code != http.StatusConflict {
		t.Errorf("status: got %d, want 409", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding conflict response: %v", err)
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("response has no 'error' object: %v", resp)
	}
	if errObj["code"] != "MERGE_REQUIRED" {
		t.Errorf("error code: got %q, want MERGE_REQUIRED", errObj["code"])
	}
	if conflictID, _ := resp["conflicting_series_id"].(float64); int(conflictID) != 42 {
		t.Errorf("conflicting_series_id: got %v, want 42", resp["conflicting_series_id"])
	}
	if resp["conflicting_series_title"] != "X-Men" {
		t.Errorf("conflicting_series_title: got %v, want X-Men", resp["conflicting_series_title"])
	}
	// requested_series_id must be absent when the caller passed 0 — a zero is not
	// a valid series ID and the frontend must not try to call /series/0/merge-into/…
	if _, present := resp["requested_series_id"]; present {
		t.Errorf("requested_series_id should be absent in body when requestedSeriesID=0, got %v", resp["requested_series_id"])
	}
}

// TestWantTrack_ConflictNotTriggered verifies that writeMatchConflict returns
// false for a plain (non-conflict) error, so the caller falls through to 500.
func TestWantTrack_ConflictNotTriggered(t *testing.T) {
	w := httptest.NewRecorder()
	handled := writeMatchConflict(w, 0, errors.New("some other error"))
	if handled {
		t.Fatal("writeMatchConflict returned true for a plain error — should be false")
	}
}
