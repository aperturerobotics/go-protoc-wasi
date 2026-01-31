# go-protoc-wasi

[![GoDoc Widget]][GoDoc] [![Go Report Card Widget]][Go Report Card]

> A Go module that embeds the Protocol Buffers compiler (protoc) as a WASI WebAssembly reactor.

[GoDoc]: https://godoc.org/github.com/aperturerobotics/go-protoc-wasi
[GoDoc Widget]: https://godoc.org/github.com/aperturerobotics/go-protoc-wasi?status.svg
[Go Report Card Widget]: https://goreportcard.com/badge/github.com/aperturerobotics/go-protoc-wasi
[Go Report Card]: https://goreportcard.com/report/github.com/aperturerobotics/go-protoc-wasi

## Related Projects

- [aperturerobotics/protobuf](https://github.com/aperturerobotics/protobuf) - Fork with WASI reactor build support (branch: `wasi`)
- [protocolbuffers/protobuf](https://github.com/protocolbuffers/protobuf) - Official Protocol Buffers repository
- [tetratelabs/wazero](https://github.com/tetratelabs/wazero) - Zero-dependency WebAssembly runtime for Go

## About

This module provides the Protocol Buffers compiler (`protoc`) compiled to WebAssembly with WASI support using the **reactor model**. The WASM binary is embedded directly in the Go module, enabling protoc to run in Go applications without external dependencies or native binaries.

### Reactor Model

Unlike the standard WASI "command" model that blocks in `_start()`, the reactor model exports functions that can be called multiple times, enabling:

- Multiple compilations per instance
- Reusable runtime without reloading the module
- Full control over the compilation lifecycle

### Exported Functions

**Protoc API:**

- `protoc_init` - Initialize the compiler and register generators
- `protoc_run` - Run protoc with command-line arguments
- `protoc_destroy` - Clean up and free resources

**Memory Management:**

- `malloc`, `free`, `realloc`, `calloc` - For host to allocate memory

### Built-in Generators

The WASM binary includes only the C++ generator (`--cpp_out`) to minimize size. All other languages (Go, Java, Python, etc.) are supported via plugins through the host's plugin handler.

### Plugin Support

External protoc plugins (like `protoc-gen-go`) are supported via host function imports. When protoc needs to communicate with a plugin, it calls the `plugin_communicate` host function, which spawns the native plugin process on the host system.

## Features

- Embeds protoc as a 3.1MB WASI WebAssembly binary
- Reactor model for multiple compilations per instance
- Plugin support via host function imports
- Virtual filesystem support for .proto files
- Thread-safe with mutex protection

## Usage

```go
package main

import (
    "bytes"
    "context"
    "fmt"
    "testing/fstest"

    "github.com/tetratelabs/wazero"
    protoc "github.com/aperturerobotics/go-protoc-wasi"
)

func main() {
    ctx := context.Background()
    r := wazero.NewRuntime(ctx)
    defer r.Close(ctx)

    // Create in-memory filesystem with .proto files
    memFS := fstest.MapFS{
        "example.proto": &fstest.MapFile{Data: []byte(`
syntax = "proto3";
package example;

message Person {
  string name = 1;
  int32 age = 2;
}
`)},
    }

    var stdout, stderr bytes.Buffer
    p, err := protoc.NewProtoc(ctx, r, &protoc.Config{
        Stdout: &stdout,
        Stderr: &stderr,
        FS:     memFS,
    })
    if err != nil {
        panic(err)
    }
    defer p.Close(ctx)

    // Initialize the compiler
    if err := p.Init(ctx); err != nil {
        panic(err)
    }

    // Show version
    exitCode, _ := p.Run(ctx, []string{"protoc", "--version"})
    fmt.Println(stdout.String())

    // Compile with a plugin (e.g., protoc-gen-go)
    // The plugin runs natively on the host via the PluginHandler
    exitCode, err = p.Run(ctx, []string{
        "protoc",
        "--go_out=/output",
        "--go_opt=paths=source_relative",
        "-I/",
        "/example.proto",
    })
    if exitCode != 0 {
        fmt.Println("Error:", stderr.String())
    }
}
```

## Configuration

```go
type Config struct {
    // Stdin is the standard input for protoc. Default: empty.
    Stdin io.Reader
    // Stdout is the standard output for protoc. Default: discard.
    Stdout io.Writer
    // Stderr is the standard error for protoc. Default: discard.
    Stderr io.Writer
    // FS is the filesystem for reading .proto files and writing output.
    FS fs.FS
    // FSConfig allows configuring the wazero filesystem.
    FSConfig wazero.FSConfig
    // PluginHandler handles spawning plugin processes.
    // Default: DefaultPluginHandler (uses os/exec).
    PluginHandler PluginHandler
}
```

## Custom Plugin Handler

The default plugin handler spawns native processes using `os/exec`. You can provide a custom handler:

```go
type PluginHandler interface {
    // Communicate spawns a plugin process and handles IPC.
    // program: plugin program name (e.g., "protoc-gen-go")
    // searchPath: if true, search PATH for the program
    // input: serialized CodeGeneratorRequest
    // Returns: serialized CodeGeneratorResponse, or error
    Communicate(ctx context.Context, program string, searchPath bool, input []byte) ([]byte, error)
}
```

## Building the WASM Binary

The WASM binary is built from [aperturerobotics/protobuf](https://github.com/aperturerobotics/protobuf) (branch: `wasi`):

```bash
# Clone the repository
git clone -b wasi https://github.com/aperturerobotics/protobuf.git
cd protobuf

# Build Abseil for WASI (required dependency)
ABSEIL_SOURCE=/path/to/abseil-cpp ./build-abseil-wasi.sh

# Build protoc for WASI
./build-wasi.sh

# Output: build-wasi/protoc.wasm (approximately 3.1MB)
```

### Build Requirements

- [WASI SDK 29.0+](https://github.com/WebAssembly/wasi-sdk)
- [Abseil C++](https://github.com/abseil/abseil-cpp)
- CMake 3.16+

## Testing

```bash
go test -v ./...
```

## License

MIT
