package blend

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestOracleStoredLayoutFixture(t *testing.T) {
	got := OracleStoredLayoutFixture()
	if len(got) == 0 {
		t.Fatal("OracleStoredLayoutFixture() returned empty bytes")
	}

	want, err := os.ReadFile(filepath.Join("testdata", "oracle_stored_layout.json"))
	if err != nil {
		t.Fatalf("read on-disk fixture: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("embedded fixture differs from testdata/oracle_stored_layout.json (embedded %d bytes, on-disk %d bytes)", len(got), len(want))
	}
}

func TestCometValuationVectorsFixture(t *testing.T) {
	got := CometValuationVectorsFixture()
	if len(got) == 0 {
		t.Fatal("CometValuationVectorsFixture() returned empty bytes")
	}

	want, err := os.ReadFile(filepath.Join("testdata", "v1_09_comet_vectors.json"))
	if err != nil {
		t.Fatalf("read on-disk fixture: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("embedded fixture differs from testdata/v1_09_comet_vectors.json (embedded %d bytes, on-disk %d bytes)", len(got), len(want))
	}
}
