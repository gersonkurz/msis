//go:build !windows

package cabinet

import "fmt"

func Extract(data []byte) (map[string][]byte, error) {
	return nil, fmt.Errorf("extracting a cabinet needs the Windows decompressor (cabinet.dll)")
}
