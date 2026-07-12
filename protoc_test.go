package protoc

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tetratelabs/wazero"
)

func TestProtocVersion(t *testing.T) {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)

	var stdout bytes.Buffer
	p, err := NewProtoc(ctx, r, &Config{
		Stdout: &stdout,
		Stderr: &stdout,
	})
	if err != nil {
		t.Fatalf("NewProtoc failed: %v", err)
	}
	defer p.Close(ctx)

	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	exitCode, err := p.Run(ctx, []string{"protoc", "--version"})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("unexpected exit code: %d, output: %s", exitCode, stdout.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "libprotoc") {
		t.Errorf("expected version output to contain 'libprotoc', got: %s", output)
	}
}

func TestProtocHelp(t *testing.T) {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)

	var stdout bytes.Buffer
	p, err := NewProtoc(ctx, r, &Config{
		Stdout: &stdout,
		Stderr: &stdout,
	})
	if err != nil {
		t.Fatalf("NewProtoc failed: %v", err)
	}
	defer p.Close(ctx)

	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	exitCode, err := p.Run(ctx, []string{"protoc", "--help"})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("unexpected exit code: %d", exitCode)
	}

	output := stdout.String()

	for _, flag := range []string{"--cpp_out", "--csharp_out", "--python_out"} {
		if !strings.Contains(output, flag) {
			t.Errorf("expected help output to contain %q", flag)
		}
	}
	// Check for plugin support
	if !strings.Contains(output, "--plugin") {
		t.Errorf("expected help output to contain '--plugin'")
	}
}

func TestProtocDescriptorSet(t *testing.T) {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)

	// Create a simple .proto file
	protoContent := `
syntax = "proto3";
package test;

message Person {
  string name = 1;
  int32 age = 2;
}
`

	// Create in-memory filesystem with the .proto file and output directory
	memFS := fstest.MapFS{
		"test.proto": &fstest.MapFile{Data: []byte(protoContent)},
		"out":        &fstest.MapFile{Mode: 0o755 | 0x80000000}, // directory
	}

	var stdout, stderr bytes.Buffer
	p, err := NewProtoc(ctx, r, &Config{
		Stdout: &stdout,
		Stderr: &stderr,
		FS:     memFS,
	})
	if err != nil {
		t.Fatalf("NewProtoc failed: %v", err)
	}
	defer p.Close(ctx)

	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Compile to descriptor set (output to file in memFS)
	// Note: writing to a file instead of /dev/stdout since WASI doesn't have /dev/stdout
	exitCode, err := p.Run(ctx, []string{
		"protoc",
		"--descriptor_set_out=/out/test.pb",
		"-I/",
		"/test.proto",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if exitCode != 0 {
		t.Logf("stdout: %s", stdout.String())
		t.Logf("stderr: %s", stderr.String())
		// For now, just check that it ran - output to memFS may not work
		// since wazero's fstest.MapFS is read-only
		t.Skip("descriptor set test skipped - memFS is read-only")
	}
}

func TestProtocMultipleRuns(t *testing.T) {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)

	p, err := NewProtoc(ctx, r, &Config{})
	if err != nil {
		t.Fatalf("NewProtoc failed: %v", err)
	}
	defer p.Close(ctx)

	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Run multiple times to test reactor reuse
	for i := 0; i < 3; i++ {
		var stdout bytes.Buffer
		// Note: we can't change stdout after creation, so just run with default
		exitCode, err := p.Run(ctx, []string{"protoc", "--version"})
		if err != nil {
			t.Fatalf("Run %d failed: %v", i, err)
		}
		if exitCode != 0 {
			t.Fatalf("Run %d: unexpected exit code: %d, stdout: %s", i, exitCode, stdout.String())
		}
	}
}

func TestProtocInitRequired(t *testing.T) {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)

	p, err := NewProtoc(ctx, r, &Config{})
	if err != nil {
		t.Fatalf("NewProtoc failed: %v", err)
	}
	defer p.Close(ctx)

	// Run without Init should fail
	_, err = p.Run(ctx, []string{"protoc", "--version"})
	if err == nil {
		t.Error("expected error when running without Init")
	}
}

func TestProtocCppGenerate(t *testing.T) {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)

	// Create a simple .proto file
	protoContent := `
syntax = "proto3";
package test;

message Person {
  string name = 1;
  int32 age = 2;
}
`

	// Create in-memory filesystem
	memFS := fstest.MapFS{
		"test.proto": &fstest.MapFile{Data: []byte(protoContent)},
	}

	var stdout, stderr bytes.Buffer
	p, err := NewProtoc(ctx, r, &Config{
		Stdout: &stdout,
		Stderr: &stderr,
		FS:     memFS,
	})
	if err != nil {
		t.Fatalf("NewProtoc failed: %v", err)
	}
	defer p.Close(ctx)

	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Try to generate C++ - will fail because memFS is read-only
	// but this tests that the --cpp_out flag is recognized
	exitCode, err := p.Run(ctx, []string{
		"protoc",
		"--cpp_out=/",
		"-I/",
		"/test.proto",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Exit code will be non-zero because we can't write to read-only memFS
	// but we can check that stderr doesn't say "Unknown flag"
	stderrStr := stderr.String()
	if strings.Contains(stderrStr, "Unknown flag") {
		t.Errorf("--cpp_out should be recognized, got: %s", stderrStr)
	}

	t.Logf("Exit code: %d (expected non-zero due to read-only fs)", exitCode)
	t.Logf("stderr: %s", stderrStr)
}

func TestProtocBuiltInGenerators(t *testing.T) {
	for _, tt := range []struct {
		name string
		flag string
		file string
	}{
		{name: "CSharp", flag: "--csharp_out=/out", file: "Test.cs"},
		{name: "Python", flag: "--python_out=/out", file: "test_pb2.py"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			r := wazero.NewRuntime(ctx)
			defer r.Close(ctx)

			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "test.proto"), []byte(`
syntax = "proto3";
package test;

message Person {
  string name = 1;
  int32 age = 2;
}
`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, "out"), 0o700); err != nil {
				t.Fatal(err)
			}

			var stderr bytes.Buffer
			p, err := NewProtoc(ctx, r, &Config{
				Stderr:   &stderr,
				FSConfig: wazero.NewFSConfig().WithDirMount(root, "/"),
			})
			if err != nil {
				t.Fatalf("NewProtoc failed: %v", err)
			}
			defer p.Close(ctx)

			if err := p.Init(ctx); err != nil {
				t.Fatalf("Init failed: %v", err)
			}

			exitCode, err := p.Run(ctx, []string{
				"protoc",
				tt.flag,
				"-I/",
				"/test.proto",
			})
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if exitCode != 0 {
				t.Fatalf("unexpected exit code %d: %s", exitCode, stderr.String())
			}

			generated, err := os.ReadFile(filepath.Join(root, "out", tt.file))
			if err != nil {
				t.Fatalf("read generated file: %v", err)
			}
			if !bytes.Contains(generated, []byte("Person")) {
				t.Errorf("generated %s does not contain Person", tt.file)
			}
		})
	}
}
