//go:build !windows

package msiread

import "fmt"

func extractCabinet(data []byte) (map[string][]byte, error) {
	return nil, fmt.Errorf("extracting a cabinet needs the Windows decompressor (cabinet.dll)")
}
