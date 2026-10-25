package bootidentity

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// TestBtrfsDefaultRootOutput accepts only the exact positive default-ID-5 record.
func TestBtrfsDefaultRootOutput(t *testing.T) {
	for _, tc := range []struct {
		name, stdout, stderr string
		err                  error
		want                 bool
	}{
		{"explicit top-level default", "ID 5 (FS_TREE)\n", "", nil, true},
		{"different default", "ID 256 gen 7 top level 5 path @root\n", "", nil, false},
		{"missing", "", "", nil, false},
		{"nonzero", "ID 5 (FS_TREE)\n", "", errors.New("exit 1"), false},
		{"diagnostic", "ID 5 (FS_TREE)\n", "warning\n", nil, false},
		{"extra line", "ID 5 (FS_TREE)\nID 256\n", "", nil, false},
		{"leading whitespace", " ID 5 (FS_TREE)\n", "", nil, false},
		{"no final newline", "ID 5 (FS_TREE)", "", nil, false},
		{"CRLF", "ID 5 (FS_TREE)\r\n", "", nil, false},
		{"NUL", "ID 5 (FS_TREE)\n\x00", "", nil, false},
		{"numeric prefix", "ID 50 (FS_TREE)\n", "", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := btrfsDefaultRootOutput([]byte(tc.stdout), []byte(tc.stderr), tc.err); got != tc.want {
				t.Fatalf("attestation = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestBtrfsOutputBound covers direct and io.Copy writes against the output cap.
func TestBtrfsOutputBound(t *testing.T) {
	buffer := &btrfsOutput{limit: 16}
	if _, err := io.Copy(buffer, strings.NewReader(strings.Repeat("x", 17))); err == nil || len(buffer.Bytes()) != 0 {
		t.Fatal("copy bypassed output bound")
	}
	if n, err := buffer.Write([]byte("1234567890123456")); err != nil || n != 16 {
		t.Fatalf("exact output bound: %d %v", n, err)
	}
	if n, err := buffer.Write([]byte("x")); err == nil || n != 0 || string(buffer.Bytes()) != "1234567890123456" {
		t.Fatal("overflow accepted or retained as success")
	}
}
