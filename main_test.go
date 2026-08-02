package main

import (
	"testing"
)

type fakeSvcErr struct{ status int }

func (fakeSvcErr) Error() string             { return "fake" }
func (f fakeSvcErr) GetHTTPStatusCode() int  { return f.status }
func (fakeSvcErr) GetMessage() string        { return "fake" }
func (fakeSvcErr) GetCode() string           { return "Fake" }
func (fakeSvcErr) GetOpcRequestID() string   { return "" }

// The guard that matters most: OCI rejects empty-string query parameters with
// 400 InvalidObjectName. Velero calls ListObjects with an empty delimiter as a
// matter of course, so passing "" straight through breaks every listing — which
// presents as "no backups exist" rather than as an error.
func TestListRequestOmitsEmptyParams(t *testing.T) {
	req := listRequest("ns", "bucket", "", "", nil, 1000)
	if req.Prefix != nil {
		t.Errorf("empty prefix must be nil, got %q", *req.Prefix)
	}
	if req.Delimiter != nil {
		t.Errorf("empty delimiter must be nil, got %q", *req.Delimiter)
	}
	if req.NamespaceName == nil || *req.NamespaceName != "ns" {
		t.Error("namespace not set")
	}
	if req.BucketName == nil || *req.BucketName != "bucket" {
		t.Error("bucket not set")
	}
	if req.Limit == nil || *req.Limit != 1000 {
		t.Error("limit not set")
	}
}

func TestListRequestKeepsNonEmptyParams(t *testing.T) {
	start := "cursor"
	req := listRequest("ns", "bucket", "backups/", "/", &start, 500)
	if req.Prefix == nil || *req.Prefix != "backups/" {
		t.Error("prefix should be passed through")
	}
	if req.Delimiter == nil || *req.Delimiter != "/" {
		t.Error("delimiter should be passed through")
	}
	if req.Start == nil || *req.Start != "cursor" {
		t.Error("pagination cursor should be passed through")
	}
}

func TestIsNotFound(t *testing.T) {
	if !isNotFound(fakeSvcErr{status: 404}) {
		t.Error("404 should be not-found")
	}
	for _, s := range []int{403, 409, 500} {
		if isNotFound(fakeSvcErr{status: s}) {
			t.Errorf("%d should not be not-found", s)
		}
	}
	// A non-service error must not be swallowed as "missing" — that would make
	// DeleteObject report success on a network failure and Velero would consider
	// an expired backup reclaimed when it was not.
	if isNotFound(errString("connection reset")) {
		t.Error("non-service errors must not count as not-found")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestConfigurationProviderRejectsUnknownMode(t *testing.T) {
	if _, err := configurationProvider("nonsense"); err == nil {
		t.Error("unknown auth mode should fail loudly rather than silently defaulting")
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "third"); got != "third" {
		t.Errorf("got %q", got)
	}
	if got := firstNonEmpty("first", "second"); got != "first" {
		t.Errorf("got %q", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("got %q", got)
	}
}
