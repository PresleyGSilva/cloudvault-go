package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// Compatibility check for any S3 endpoint (MinIO or S3Mock): presigned up/down round-trip.
func TestS3PresignedRoundTrip(t *testing.T) {
	ep := os.Getenv("TEST_S3_ENDPOINT")
	if ep == "" {
		ep = "localhost:9090"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st, err := New(ep, "minioadmin", "minioadmin", "compat-test", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureBucket(ctx); err != nil {
		t.Skipf("no S3 endpoint at %s: %v", ep, err)
	}
	key := "roundtrip/hello.txt"
	putURL, err := st.PresignPut(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("s3 round-trip payload")
	req, _ := http.NewRequestWithContext(ctx, "PUT", putURL, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("presigned PUT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("presigned PUT status %d", resp.StatusCode)
	}
	size, _, err := st.Stat(ctx, key)
	if err != nil || size != int64(len(payload)) {
		t.Fatalf("stat: size=%d err=%v", size, err)
	}
	getURL, err := st.PresignGet(ctx, key, false, "hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	gresp, err := http.Get(getURL)
	if err != nil {
		t.Fatalf("presigned GET: %v", err)
	}
	defer gresp.Body.Close()
	got, _ := io.ReadAll(gresp.Body)
	if !bytes.Equal(got, payload) {
		t.Fatalf("mismatch: %q", got)
	}
	_ = st.Remove(ctx, key)
}
