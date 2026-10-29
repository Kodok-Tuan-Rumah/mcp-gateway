package audit

import (
	"strings"
	"testing"
)

func TestGenerateShareToken_Is128Bit(t *testing.T) {
	token, err := GenerateShareToken()
	if err != nil {
		t.Fatalf("GenerateShareToken: %v", err)
	}
	if len(token) != 32 {
		t.Fatalf("token length: got %d want 32 (16 bytes = 128 bits hex-encoded)", len(token))
	}
	for _, c := range token {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("token contains non-hex char: %q", c)
		}
	}
}
