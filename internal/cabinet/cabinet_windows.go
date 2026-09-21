//go:build windows

package cabinet

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Cabinet extraction, through Windows' own FDI decompressor in cabinet.dll.
//
// Hand-rolling this was considered and rejected. A released msis cabinet is LZX-compressed
// (folder compression type 0x1203) because WiX defaults to high compression, and LZX is a
// substantial algorithm - Huffman trees, aligned offsets, E8 call translation - whose subtle
// bugs would surface as silently wrong bytes, which in an SBOM means silently wrong hashes.
// MSZIP alone would have been a few lines over compress/flate, but MSZIP alone is not what
// packages contain.
//
// Nothing here executes the package: FDI decompresses a byte stream. That is a different thing
// from `msiexec /a`, which runs AdminExecuteSequence.
//
// Extraction is entirely in memory. The alternative - spilling to a temp directory and reading
// back - would put payload on disk for no reason, and the bytes are wanted only to be hashed.
var (
	cabinetDLL  = windows.NewLazySystemDLL("cabinet.dll")
	procFDICrt  = cabinetDLL.NewProc("FDICreate")
	procFDICopy = cabinetDLL.NewProc("FDICopy")
	procFDIDstr = cabinetDLL.NewProc("FDIDestroy")

	kernel32      = windows.NewLazySystemDLL("kernel32.dll")
	procGetHeap   = kernel32.NewProc("GetProcessHeap")
	procHeapAlloc = kernel32.NewProc("HeapAlloc")
	procHeapFree  = kernel32.NewProc("HeapFree")
)

// FDI notification types (fdi.h), spelled out: an omitted expression in a Go const block
// repeats the previous one rather than counting on, which quietly made CLOSE_FILE_INFO equal
// COPY_FILE.
const (
	fdintCABINET_INFO    int32 = 0
	fdintPARTIAL_FILE    int32 = 1
	fdintCOPY_FILE       int32 = 2
	fdintCLOSE_FILE_INFO int32 = 3
	fdintNEXT_CABINET    int32 = 4
	fdintENUMERATE       int32 = 5
)

// erf is FDI's error record: {int erfOper; int erfType; BOOL fError;}.
type erf struct {
	oper   int32
	typ    int32
	fError int32
}

// fdiNotification mirrors FDINOTIFICATION. Only the fields this code reads are named
// meaningfully; the layout must match exactly, so the rest are kept as placeholders.
type fdiNotification struct {
	// No hand-written padding: Go aligns the pointer that follows to the platform's pointer
	// size, which is what the SDK layout wants - offset 4 on 386, offset 8 on amd64. An
	// explicit int32 pad hard-codes the amd64 answer and displaces every field after it,
	// including hf, on the 386 build msis ships.
	cb       int32
	psz1     *byte
	psz2     *byte
	psz3     *byte
	pv       unsafe.Pointer
	hf       uintptr
	date     uint16
	time     uint16
	attribs  uint16
	setID    uint16
	iCabinet uint16
	iFolder  uint16
	fdie     int32
}

// openFile is one "file" the FDI callbacks operate on: either the cabinet being read or an
// extracted payload being written. Both are memory.
type openFile struct {
	data []byte // the cabinet's bytes, for reads
	pos  int
	out  []byte // accumulated decompressed bytes, for writes
	name string
}

// FDI's callbacks are plain C function pointers with no user context, so the state they act on
// has to be reachable from a package-level registry keyed by the handle FDI passes back. The
// handles come from a counter rather than from a pointer, so two concurrent extractions cannot
// collide - Read is called concurrently in this package's own tests.
var (
	mu        sync.Mutex
	openFiles         = map[uintptr]*openFile{}
	nextID    uintptr = 1
	// Extraction is serialised on extractMu so one session's callbacks cannot see
	// another's; source is the cabinet the current session is reading and result
	// collects what comes out of it.
	extractMu sync.Mutex
	source    []byte
	result    map[string][]byte
	spanned   bool
)

func register(f *openFile) uintptr {
	mu.Lock()
	defer mu.Unlock()
	id := nextID
	nextID++
	openFiles[id] = f
	return id
}

func lookup(h uintptr) *openFile {
	mu.Lock()
	defer mu.Unlock()
	return openFiles[h]
}

func release(h uintptr) {
	mu.Lock()
	defer mu.Unlock()
	delete(openFiles, h)
}

// The callbacks. They are cdecl (fdi.h declares FNALLOC and friends __cdecl), so they must be
// created with NewCallbackCDecl: on 386 a stdcall callback would corrupt the stack, and msis
// ships a 386 build.
var (
	cbAlloc = syscall.NewCallbackCDecl(func(cb uint32) uintptr {
		heap, _, _ := procGetHeap.Call()
		p, _, _ := procHeapAlloc.Call(heap, 0, uintptr(cb))
		return p
	})

	cbFree = syscall.NewCallbackCDecl(func(p uintptr) uintptr {
		if p != 0 {
			heap, _, _ := procGetHeap.Call()
			procHeapFree.Call(heap, 0, p)
		}
		return 0
	})

	// open is only ever called for the cabinet, which is already in memory; there is no
	// filesystem access at any point.
	//
	// Each open gets its OWN cursor over the same bytes. FDI keeps two streams on a cabinet at
	// once - one walking the CFFILE list, one reading CFDATA blocks - and handing both the same
	// openFile made them clobber each other's position, which FDI reports as a corrupt
	// cabinet. The bytes are shared read-only; only the offset is per-handle.
	cbOpen = syscall.NewCallbackCDecl(func(name *byte, oflag int32, pmode int32) uintptr {
		if source == nil {
			return ^uintptr(0) // -1: FDI's "cannot open"
		}
		return register(&openFile{data: source, name: "<cabinet>"})
	})

	cbRead = syscall.NewCallbackCDecl(func(h uintptr, pv unsafe.Pointer, cb uint32) uintptr {
		f := lookup(h)
		if f == nil || f.data == nil {
			return ^uintptr(0)
		}
		n := len(f.data) - f.pos
		if n > int(cb) {
			n = int(cb)
		}
		if n > 0 {
			copy(unsafe.Slice((*byte)(pv), n), f.data[f.pos:f.pos+n])
			f.pos += n
		}
		return uintptr(n)
	})

	cbWrite = syscall.NewCallbackCDecl(func(h uintptr, pv unsafe.Pointer, cb uint32) uintptr {
		f := lookup(h)
		if f == nil {
			return ^uintptr(0)
		}
		if cb > 0 {
			f.out = append(f.out, unsafe.Slice((*byte)(pv), cb)...)
		}
		return uintptr(cb)
	})

	// An extracted payload is captured on whichever of PFNCLOSE and fdintCLOSE_FILE_INFO
	// arrives first - FDI's order between them is not something to depend on - and the second
	// finds nothing registered and does nothing.
	//
	// Every handle is released here, the cabinet's included. Releasing only the output handles
	// left each extraction's compressed buffer in the registry for the life of the process.
	cbClose = syscall.NewCallbackCDecl(func(h uintptr) uintptr {
		if f := lookup(h); f != nil {
			if f.data == nil {
				result[f.name] = f.out
			}
			release(h)
		}
		return 0
	})

	cbSeek = syscall.NewCallbackCDecl(func(h uintptr, dist int32, seektype int32) uintptr {
		f := lookup(h)
		if f == nil || f.data == nil {
			return ^uintptr(0)
		}
		switch seektype {
		case 0: // SEEK_SET
			f.pos = int(dist)
		case 1: // SEEK_CUR
			f.pos += int(dist)
		case 2: // SEEK_END
			f.pos = len(f.data) + int(dist)
		}
		if f.pos < 0 {
			f.pos = 0
		}
		if f.pos > len(f.data) {
			f.pos = len(f.data)
		}
		return uintptr(f.pos)
	})

	// notify receives each entry. Returning a handle from fdintCOPY_FILE asks FDI to extract
	// it; returning 0 skips it.
	cbNotify = syscall.NewCallbackCDecl(func(fdint int32, n *fdiNotification) uintptr {
		return handleNotify(fdint, n)
	})
)

// cstring reads a NUL-terminated byte string FDI handed us.
func cstring(p *byte) string {
	if p == nil {
		return ""
	}
	var b []byte
	for q := unsafe.Pointer(p); ; q = unsafe.Add(q, 1) {
		c := *(*byte)(q)
		if c == 0 {
			return string(b)
		}
		b = append(b, c)
	}
}

// Extract decompresses a cabinet held in memory and returns each entry's bytes by name.
//
// In a cabinet WiX produced, entry names are the File table's keys (FILE_ID00007 and so on),
// which is what makes mapping payload to inventory rows exact rather than a guess at filenames.
func Extract(data []byte) (map[string][]byte, error) {
	if len(data) < 4 || string(data[:4]) != "MSCF" {
		return nil, fmt.Errorf("not a cabinet: missing the MSCF signature")
	}

	// FDI's callbacks carry no user context, so one extraction at a time.
	extractMu.Lock()
	defer extractMu.Unlock()

	var e erf
	hfdi, _, _ := procFDICrt.Call(
		cbAlloc, cbFree, cbOpen, cbRead, cbWrite, cbClose, cbSeek,
		uintptr(0xFFFFFFFF), // cpuUNKNOWN; ignored by modern cabinet.dll
		uintptr(unsafe.Pointer(&e)),
	)
	if hfdi == 0 {
		return nil, fmt.Errorf("FDICreate failed (erf oper=%d type=%d)", e.oper, e.typ)
	}
	defer procFDIDstr.Call(hfdi)

	source = data
	result = map[string][]byte{}
	spanned = false

	// Whatever happens, no handle from this session outlives it. FDI closes what it opens on
	// a clean run; an aborted one can leave handles behind, and they hold the cabinet buffer.
	firstHandle := peekNextID()
	defer func() {
		releaseFrom(firstHandle)
		source, result = nil, nil
	}()

	// The names are handed to our open callback, which ignores them and returns the cabinet
	// already in memory; nothing is looked up on disk.
	name, _ := syscall.BytePtrFromString("cabinet.cab")
	path, _ := syscall.BytePtrFromString(".\\")
	ok, _, _ := procFDICopy.Call(
		hfdi,
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(path)),
		0,
		cbNotify,
		0,
		0,
	)
	if ok == 0 {
		if spanned {
			return nil, fmt.Errorf("this package's payload spans several cabinets, which msis " +
				"cannot read: only the cabinet embedded in the package is available")
		}
		return nil, fmt.Errorf("FDICopy failed to extract the cabinet (erf oper=%d type=%d)", e.oper, e.typ)
	}

	out := result
	result = nil
	return out, nil
}

// handleNotify is the notification logic, separated from the callback so it can be called
// directly from a test. A spanned cabinet is otherwise only reachable by building one.
func handleNotify(fdint int32, n *fdiNotification) uintptr {
	switch fdint {
	case fdintCOPY_FILE:
		return register(&openFile{name: cstring(n.psz1)})

	case fdintCLOSE_FILE_INFO:
		if f := lookup(n.hf); f != nil {
			result[f.name] = f.out
			release(n.hf)
		}
		return 1 // TRUE: continue

	case fdintNEXT_CABINET:
		// A spanned set: FDI is asking for the next cabinet in the sequence. This reader has
		// one cabinet in memory and no way to find a continuation, and the open callback
		// would hand back the same bytes again - which FDI retries indefinitely, holding the
		// extraction lock with it. Abort instead, and say so.
		spanned = true
		return ^uintptr(0) // -1: abort

	default:
		return 0
	}
}

// peekNextID reports the id the next registration will use, so a session can tell which
// handles are its own.
func peekNextID() uintptr {
	mu.Lock()
	defer mu.Unlock()
	return nextID
}

// releaseFrom drops every handle issued at or after from. Used to guarantee an extraction
// leaves the registry as it found it, including after a failure.
func releaseFrom(from uintptr) {
	mu.Lock()
	defer mu.Unlock()
	for id := range openFiles {
		if id >= from {
			delete(openFiles, id)
		}
	}
}

// registrySize is for tests: the number of handles currently held.
func registrySize() int {
	mu.Lock()
	defer mu.Unlock()
	return len(openFiles)
}
