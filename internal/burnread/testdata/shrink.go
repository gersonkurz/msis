//go:build ignore

// shrink zeroes the engine code sections of a built bundle so the fixture is cheap to commit.
//
// A Burn bundle is ~800 kB, almost all of it the engine stub's .text and .rdata. The reader
// never looks at those bytes - it reads the PE headers, the .wixburn section, and the cabinets
// appended after the stub - so zeroing them changes nothing it can observe while making the
// file compress to a few kilobytes in git. It also makes the fixture plainly non-runnable,
// which is the right property for a file that exists to be parsed.
//
// Offsets are left exactly as they were: the containers are still where the header says, which
// is the whole point of the fixture.
//
// Usage: go run shrink.go fixture.exe
package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
)

// The sections the reader never touches. .wixburn, .data and .didat are left alone; so is
// everything before the first section, which holds the PE headers.
var dead = map[string]bool{".text": true, ".rdata": true, ".reloc": true, ".rsrc": true}

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: go run shrink.go <bundle.exe>")
	}
	path := os.Args[1]
	raw, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}

	peOff := int(binary.LittleEndian.Uint32(raw[0x3c:]))
	sections := int(binary.LittleEndian.Uint16(raw[peOff+6:]))
	optSize := int(binary.LittleEndian.Uint16(raw[peOff+20:]))
	table := peOff + 24 + optSize

	zeroed := 0
	for i := 0; i < sections; i++ {
		e := table + i*40
		name := string(trimNul(raw[e : e+8]))
		size := int(binary.LittleEndian.Uint32(raw[e+16:]))
		ptr := int(binary.LittleEndian.Uint32(raw[e+20:]))
		if !dead[name] || ptr == 0 || ptr+size > len(raw) {
			continue
		}
		for j := ptr; j < ptr+size; j++ {
			raw[j] = 0
		}
		zeroed += size
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("zeroed %d of %d bytes in %s\n", zeroed, len(raw), path)
}

func trimNul(b []byte) []byte {
	for i, c := range b {
		if c == 0 {
			return b[:i]
		}
	}
	return b
}
