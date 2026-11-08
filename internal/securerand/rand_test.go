package securerand

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestSourceUint64(t *testing.T) {
	// 修改 crypto/rand.Reader 的测试不能并行运行。
	original := rand.Reader
	t.Cleanup(func() { rand.Reader = original })
	reader := bytes.NewReader([]byte{
		1, 2, 3, 4, 5, 6, 7, 8,
		9, 10, 11, 12, 13, 14, 15, 16,
	})
	rand.Reader = reader
	rng := New()
	for _, want := range []uint64{0x0807060504030201, 0x100f0e0d0c0b0a09} {
		if got := rng.Uint64(); got != want {
			t.Fatalf("Uint64() = %#x, want %#x", got, want)
		}
	}
	if reader.Len() != 0 {
		t.Fatal("each Uint64 call must consume exactly 8 bytes")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("test entropy failure")
}

func TestSourceFailure(t *testing.T) {
	// Go 1.26 的 crypto/rand.Read 会 fatal，必须在子进程中验证。
	if os.Getenv("SECURERAND_TEST_FAILURE") == "1" {
		rand.Reader = failingReader{}
		New().Uint64()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSourceFailure$")
	cmd.Env = append(os.Environ(), "SECURERAND_TEST_FAILURE=1")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("random source failure must terminate without fallback")
	}
	if !strings.Contains(string(output), "test entropy failure") {
		t.Fatalf("unexpected subprocess failure: %v\n%s", err, output)
	}
}
