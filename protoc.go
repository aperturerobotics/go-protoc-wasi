package protoc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Protoc wraps a protoc WASI reactor module providing a high-level API
// for Protocol Buffer compilation.
type Protoc struct {
	runtime wazero.Runtime
	mod     api.Module

	// Memory management
	malloc api.Function
	free   api.Function

	// Protoc reactor functions
	protocInit    api.Function
	protocRun     api.Function
	protocDestroy api.Function

	// Plugin handler for spawning native plugin processes
	pluginHandler PluginHandler

	// Mutex for thread-safe Run calls (WASI is single-threaded)
	mu sync.Mutex

	// State
	initialized bool
}

// PluginHandler handles spawning and communicating with protoc plugins.
// The default implementation uses os/exec to spawn native processes.
type PluginHandler interface {
	// Communicate spawns a plugin process and handles IPC.
	// program: plugin program name (e.g., "protoc-gen-go")
	// searchPath: if true, search PATH for the program
	// input: serialized CodeGeneratorRequest
	// Returns: serialized CodeGeneratorResponse, or error
	Communicate(ctx context.Context, program string, searchPath bool, input []byte) ([]byte, error)
}

// DefaultPluginHandler spawns plugin processes using os/exec.
type DefaultPluginHandler struct{}

// Communicate spawns a plugin and communicates via stdin/stdout.
func (h *DefaultPluginHandler) Communicate(ctx context.Context, program string, searchPath bool, input []byte) ([]byte, error) {
	var cmd *exec.Cmd
	if searchPath {
		cmd = exec.CommandContext(ctx, program)
	} else {
		cmd = exec.CommandContext(ctx, program)
	}

	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("%s: %w: %s", program, err, stderr.String())
		}
		return nil, fmt.Errorf("%s: %w", program, err)
	}

	return stdout.Bytes(), nil
}

// Config holds configuration for creating a new Protoc instance.
type Config struct {
	// Stdin is the standard input for protoc. Default: empty.
	Stdin io.Reader
	// Stdout is the standard output for protoc. Default: discard.
	Stdout io.Writer
	// Stderr is the standard error for protoc. Default: discard.
	Stderr io.Writer
	// FS is the filesystem for reading .proto files and writing output.
	// Default: no filesystem access.
	FS fs.FS
	// FSConfig allows configuring the wazero filesystem.
	// If set, FS is ignored.
	FSConfig wazero.FSConfig
	// PluginHandler handles spawning plugin processes.
	// Default: DefaultPluginHandler (uses os/exec).
	PluginHandler PluginHandler
}

// CompileProtoc compiles the embedded protoc WASM module.
// The compiled module can be reused across multiple Protoc instances.
func CompileProtoc(ctx context.Context, r wazero.Runtime) (wazero.CompiledModule, error) {
	return r.CompileModule(ctx, ProtocWASM)
}

// NewProtoc creates a new Protoc instance using the embedded WASM reactor.
// Call Close() when done to release resources.
func NewProtoc(ctx context.Context, r wazero.Runtime, cfg *Config) (*Protoc, error) {
	// Compile the module
	compiled, err := CompileProtoc(ctx, r)
	if err != nil {
		return nil, err
	}

	return NewProtocWithModule(ctx, r, compiled, cfg)
}

// NewProtocWithModule creates a new Protoc instance using a pre-compiled module.
func NewProtocWithModule(ctx context.Context, r wazero.Runtime, compiled wazero.CompiledModule, cfg *Config) (*Protoc, error) {
	if cfg == nil {
		cfg = &Config{}
	}

	// Set up plugin handler
	pluginHandler := cfg.PluginHandler
	if pluginHandler == nil {
		pluginHandler = &DefaultPluginHandler{}
	}

	// Create the Protoc instance first so we can reference it in host functions
	p := &Protoc{
		runtime:       r,
		pluginHandler: pluginHandler,
	}

	// Register host functions for plugin communication
	_, err := r.NewHostModuleBuilder(ImportModuleProtoc).
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, mod api.Module, stack []uint64) {
			p.hostPluginCommunicate(ctx, mod, stack)
		}), []api.ValueType{
			api.ValueTypeI32, // program_ptr
			api.ValueTypeI32, // program_len
			api.ValueTypeI32, // search_path
			api.ValueTypeI32, // input_ptr
			api.ValueTypeI32, // input_len
			api.ValueTypeI32, // output_ptr (pointer to pointer)
			api.ValueTypeI32, // output_len (pointer to uint32)
			api.ValueTypeI32, // error_ptr (pointer to pointer)
			api.ValueTypeI32, // error_len (pointer to uint32)
		}, []api.ValueType{api.ValueTypeI32}).
		Export(ImportPluginCommunicate).
		Instantiate(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to register host functions: %w", err)
	}

	// Instantiate WASI
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		return nil, fmt.Errorf("failed to instantiate WASI: %w", err)
	}

	// Build module config
	modCfg := wazero.NewModuleConfig().WithName(ProtocWASMFilename)

	if cfg.Stdin != nil {
		modCfg = modCfg.WithStdin(cfg.Stdin)
	}
	if cfg.Stdout != nil {
		modCfg = modCfg.WithStdout(cfg.Stdout)
	}
	if cfg.Stderr != nil {
		modCfg = modCfg.WithStderr(cfg.Stderr)
	}

	if cfg.FSConfig != nil {
		modCfg = modCfg.WithFSConfig(cfg.FSConfig)
	} else if cfg.FS != nil {
		modCfg = modCfg.WithFSConfig(wazero.NewFSConfig().WithFSMount(cfg.FS, "/"))
	}

	// Instantiate the module (reactor mode - no _start)
	mod, err := r.InstantiateModule(ctx, compiled, modCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate module: %w", err)
	}

	// Call _initialize if present
	if initFn := mod.ExportedFunction("_initialize"); initFn != nil {
		if _, err := initFn.Call(ctx); err != nil {
			mod.Close(ctx)
			return nil, fmt.Errorf("_initialize failed: %w", err)
		}
	}

	p.mod = mod
	p.malloc = mod.ExportedFunction(ExportMalloc)
	p.free = mod.ExportedFunction(ExportFree)
	p.protocInit = mod.ExportedFunction(ExportProtocInit)
	p.protocRun = mod.ExportedFunction(ExportProtocRun)
	p.protocDestroy = mod.ExportedFunction(ExportProtocDestroy)

	// Validate required exports
	if p.malloc == nil {
		mod.Close(ctx)
		return nil, errors.New("missing export: " + ExportMalloc)
	}
	if p.free == nil {
		mod.Close(ctx)
		return nil, errors.New("missing export: " + ExportFree)
	}
	if p.protocInit == nil {
		mod.Close(ctx)
		return nil, errors.New("missing export: " + ExportProtocInit)
	}
	if p.protocRun == nil {
		mod.Close(ctx)
		return nil, errors.New("missing export: " + ExportProtocRun)
	}
	if p.protocDestroy == nil {
		mod.Close(ctx)
		return nil, errors.New("missing export: " + ExportProtocDestroy)
	}

	return p, nil
}

// hostPluginCommunicate handles plugin subprocess communication from WASM.
func (p *Protoc) hostPluginCommunicate(ctx context.Context, mod api.Module, stack []uint64) {
	programPtr := uint32(stack[0])
	programLen := uint32(stack[1])
	searchPath := int32(stack[2]) != 0
	inputPtr := uint32(stack[3])
	inputLen := uint32(stack[4])
	outputPtrPtr := uint32(stack[5])
	outputLenPtr := uint32(stack[6])
	errorPtrPtr := uint32(stack[7])
	errorLenPtr := uint32(stack[8])

	mem := mod.Memory()

	// Read program name
	programBytes, ok := mem.Read(programPtr, programLen)
	if !ok {
		stack[0] = api.EncodeI32(-1)
		return
	}
	program := string(programBytes)

	// Read input data
	inputData, ok := mem.Read(inputPtr, inputLen)
	if !ok {
		stack[0] = api.EncodeI32(-1)
		return
	}

	// Call the plugin handler
	output, err := p.pluginHandler.Communicate(ctx, program, searchPath, inputData)

	if err != nil {
		// Write error message
		errMsg := err.Error()
		errPtr, allocErr := p.allocBytes(ctx, []byte(errMsg))
		if allocErr == nil {
			p.writePtr(mem, errorPtrPtr, errPtr)
			p.writeUint32(mem, errorLenPtr, uint32(len(errMsg)))
		}
		p.writePtr(mem, outputPtrPtr, 0)
		p.writeUint32(mem, outputLenPtr, 0)
		stack[0] = api.EncodeI32(1)
		return
	}

	// Write output
	outPtr, allocErr := p.allocBytes(ctx, output)
	if allocErr != nil {
		stack[0] = api.EncodeI32(-1)
		return
	}
	p.writePtr(mem, outputPtrPtr, outPtr)
	p.writeUint32(mem, outputLenPtr, uint32(len(output)))
	p.writePtr(mem, errorPtrPtr, 0)
	p.writeUint32(mem, errorLenPtr, 0)
	stack[0] = 0
}

// Init initializes the protoc reactor.
// This must be called before Run.
func (p *Protoc) Init(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.initialized {
		return nil
	}

	results, err := p.protocInit.Call(ctx)
	if err != nil {
		return fmt.Errorf("protoc_init failed: %w", err)
	}
	if int32(results[0]) != 0 {
		return errors.New("protoc_init returned error")
	}

	p.initialized = true
	return nil
}

// Run runs protoc with the given arguments.
// Init() must be called first.
// Returns the protoc exit code (0 on success).
func (p *Protoc) Run(ctx context.Context, args []string) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.initialized {
		return 1, errors.New("protoc not initialized, call Init() first")
	}

	if len(args) == 0 {
		args = []string{"protoc"}
	}

	// Allocate argv
	argc := len(args)
	argPtrs := make([]uint32, argc)

	for i, arg := range args {
		ptr, err := p.allocString(ctx, arg)
		if err != nil {
			// Free already allocated
			for j := 0; j < i; j++ {
				p.freePtr(ctx, argPtrs[j])
			}
			return 1, err
		}
		argPtrs[i] = ptr
	}

	// Allocate argv array
	argvPtr, err := p.allocArgv(ctx, argPtrs)
	if err != nil {
		for _, ptr := range argPtrs {
			p.freePtr(ctx, ptr)
		}
		return 1, err
	}

	// Call protoc_run
	results, err := p.protocRun.Call(ctx, uint64(argc), uint64(argvPtr))

	// Free memory
	p.freePtr(ctx, argvPtr)
	for _, ptr := range argPtrs {
		p.freePtr(ctx, ptr)
	}

	if err != nil {
		return 1, fmt.Errorf("protoc_run failed: %w", err)
	}

	return int(int32(results[0])), nil
}

// Close destroys the protoc reactor and releases resources.
func (p *Protoc) Close(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.initialized && p.protocDestroy != nil {
		p.protocDestroy.Call(ctx)
		p.initialized = false
	}

	if p.mod != nil {
		return p.mod.Close(ctx)
	}
	return nil
}

// Memory helpers

func (p *Protoc) allocString(ctx context.Context, s string) (uint32, error) {
	bytes := append([]byte(s), 0) // null-terminated
	return p.allocBytes(ctx, bytes)
}

func (p *Protoc) allocBytes(ctx context.Context, data []byte) (uint32, error) {
	results, err := p.malloc.Call(ctx, uint64(len(data)))
	if err != nil {
		return 0, err
	}
	ptr := uint32(results[0])
	if ptr == 0 {
		return 0, errors.New("malloc returned null")
	}
	if !p.mod.Memory().Write(ptr, data) {
		p.free.Call(ctx, uint64(ptr))
		return 0, errors.New("failed to write to memory")
	}
	return ptr, nil
}

func (p *Protoc) allocArgv(ctx context.Context, ptrs []uint32) (uint32, error) {
	size := len(ptrs) * 4
	results, err := p.malloc.Call(ctx, uint64(size))
	if err != nil {
		return 0, err
	}
	argvPtr := uint32(results[0])
	if argvPtr == 0 {
		return 0, errors.New("malloc returned null for argv")
	}

	for i, ptr := range ptrs {
		ptrBytes := make([]byte, 4)
		binary.LittleEndian.PutUint32(ptrBytes, ptr)
		p.mod.Memory().Write(argvPtr+uint32(i*4), ptrBytes)
	}

	return argvPtr, nil
}

func (p *Protoc) freePtr(ctx context.Context, ptr uint32) {
	if ptr != 0 {
		p.free.Call(ctx, uint64(ptr))
	}
}

func (p *Protoc) writePtr(mem api.Memory, addr, value uint32) {
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, value)
	mem.Write(addr, buf)
}

func (p *Protoc) writeUint32(mem api.Memory, addr, value uint32) {
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, value)
	mem.Write(addr, buf)
}
