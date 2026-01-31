// Package protoc provides a Go wrapper for running protoc via WASI/wazero.
package protoc

import _ "embed"

// ProtocWASM contains the binary contents of the protoc WASI reactor build.
//
// This is a reactor-model WASM that exports the protoc compiler API for
// reentrant execution in host environments. The reactor model allows multiple
// compilations per instance without reloading the module.
//
//go:embed protoc.wasm
var ProtocWASM []byte

// ProtocWASMFilename is the filename for ProtocWASM.
const ProtocWASMFilename = "protoc.wasm"

// Protoc reactor exports
const (
	// ExportProtocInit initializes the protoc reactor.
	// Creates CLI instance and registers all built-in generators.
	// Signature: protoc_init() -> i32
	// Returns: 0 on success, non-zero on error
	ExportProtocInit = "protoc_init"

	// ExportProtocRun runs protoc with the given arguments.
	// protoc_init() must be called first.
	// Signature: protoc_run(argc: i32, argv: i32) -> i32
	// Returns: protoc exit code (0 on success)
	ExportProtocRun = "protoc_run"

	// ExportProtocDestroy destroys the protoc reactor and frees resources.
	// Signature: protoc_destroy() -> void
	ExportProtocDestroy = "protoc_destroy"
)

// Memory management exports
const (
	// ExportMalloc allocates memory in WASM linear memory.
	// Signature: malloc(size: i32) -> i32 (pointer)
	ExportMalloc = "malloc"

	// ExportFree frees memory in WASM linear memory.
	// Signature: free(ptr: i32) -> void
	ExportFree = "free"

	// ExportRealloc reallocates memory in WASM linear memory.
	// Signature: realloc(ptr: i32, size: i32) -> i32 (pointer)
	ExportRealloc = "realloc"

	// ExportCalloc allocates zeroed memory in WASM linear memory.
	// Signature: calloc(nmemb: i32, size: i32) -> i32 (pointer)
	ExportCalloc = "calloc"
)

// Host import module name for plugin subprocess communication
const (
	// ImportModuleProtoc is the import module name for protoc host functions.
	ImportModuleProtoc = "protoc"

	// ImportPluginCommunicate is the host function for plugin subprocess IPC.
	// This allows protoc to spawn plugin processes on the host.
	ImportPluginCommunicate = "plugin_communicate"
)
