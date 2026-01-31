// Example demonstrates using go-protoc-wasi to compile .proto files.
package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"testing/fstest"

	"github.com/tetratelabs/wazero"
	protoc "github.com/aperturerobotics/go-protoc-wasi"
)

func main() {
	ctx := context.Background()

	// Create wazero runtime
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)

	// Example 1: Show version
	fmt.Println("=== protoc --version ===")
	if err := showVersion(ctx, r); err != nil {
		log.Fatal(err)
	}

	// Example 2: Compile a .proto file to descriptor set
	fmt.Println("\n=== Compile .proto to descriptor set ===")
	if err := compileProto(ctx, r); err != nil {
		log.Fatal(err)
	}

	// Example 3: Show help
	fmt.Println("\n=== protoc --help (truncated) ===")
	if err := showHelp(ctx, r); err != nil {
		log.Fatal(err)
	}
}

func showVersion(ctx context.Context, r wazero.Runtime) error {
	var stdout bytes.Buffer
	p, err := protoc.NewProtoc(ctx, r, &protoc.Config{
		Stdout: &stdout,
		Stderr: os.Stderr,
	})
	if err != nil {
		return err
	}
	defer p.Close(ctx)

	if err := p.Init(ctx); err != nil {
		return err
	}

	exitCode, err := p.Run(ctx, []string{"protoc", "--version"})
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("protoc exited with code %d", exitCode)
	}

	fmt.Print(stdout.String())
	return nil
}

func compileProto(ctx context.Context, r wazero.Runtime) error {
	// Create a sample .proto file in memory
	protoContent := `
syntax = "proto3";
package example;

// A simple message for demonstration
message Person {
  string name = 1;
  int32 age = 2;
  repeated string emails = 3;
}

message AddressBook {
  repeated Person people = 1;
}
`

	// Create in-memory filesystem with the .proto file
	memFS := fstest.MapFS{
		"example.proto": &fstest.MapFile{Data: []byte(protoContent)},
	}

	var stdout, stderr bytes.Buffer
	p, err := protoc.NewProtoc(ctx, r, &protoc.Config{
		Stdout: &stdout,
		Stderr: &stderr,
		FS:     memFS,
	})
	if err != nil {
		return err
	}
	defer p.Close(ctx)

	if err := p.Init(ctx); err != nil {
		return err
	}

	// Compile to descriptor set
	exitCode, err := p.Run(ctx, []string{
		"protoc",
		"--descriptor_set_out=/dev/stdout",
		"-I/",
		"/example.proto",
	})
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("protoc exited with code %d: %s", exitCode, stderr.String())
	}

	fmt.Printf("Generated descriptor set: %d bytes\n", stdout.Len())
	return nil
}

func showHelp(ctx context.Context, r wazero.Runtime) error {
	var stdout bytes.Buffer
	p, err := protoc.NewProtoc(ctx, r, &protoc.Config{
		Stdout: &stdout,
		Stderr: os.Stderr,
	})
	if err != nil {
		return err
	}
	defer p.Close(ctx)

	if err := p.Init(ctx); err != nil {
		return err
	}

	exitCode, err := p.Run(ctx, []string{"protoc", "--help"})
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("protoc exited with code %d", exitCode)
	}

	// Show first 20 lines
	lines := bytes.Split(stdout.Bytes(), []byte("\n"))
	for i, line := range lines {
		if i >= 20 {
			fmt.Println("... (truncated)")
			break
		}
		fmt.Println(string(line))
	}
	return nil
}
