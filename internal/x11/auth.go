package x11

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
)

const (
	familyInternet  = 0
	familyLocal     = 256
	familyWild      = 65535
	familyInternet6 = 6
)

type authEntry struct {
	family uint16
	addr   string
	number string
	name   string
	data   []byte
}

// readXAuthority locates an MIT-MAGIC-COOKIE-1 (or other) entry matching displayNum
// from $XAUTHORITY or ~/.Xauthority. Returns empty strings if none found.
func readXAuthority(host, displayNum string) (name string, data []byte) {
	authPath := os.Getenv("XAUTHORITY")
	if authPath == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", nil
		}
		authPath = filepath.Join(home, ".Xauthority")
	}

	f, err := os.Open(authPath)
	if err != nil {
		return "", nil
	}
	defer f.Close()

	entries := parseXAuthority(f)
	if len(entries) == 0 {
		return "", nil
	}

	hostname, _ := os.Hostname()
	isLocal := host == "" || host == "localhost" || host == "unix"

	// Pass 1: exact match on family + host/hostname + display number
	for _, e := range entries {
		if e.number != "" && e.number != displayNum {
			continue
		}
		if isLocal && (e.family == familyLocal || e.family == familyWild) {
			if e.family == familyWild || e.addr == hostname || e.addr == "" {
				return e.name, e.data
			}
		}
		if !isLocal && (e.family == familyInternet || e.family == familyInternet6 || e.family == familyWild) {
			return e.name, e.data
		}
	}

	// Pass 2: match display number (handles cases where hostname changed or containerised)
	for _, e := range entries {
		if e.number == displayNum || e.number == "" {
			return e.name, e.data
		}
	}

	return "", nil
}

func parseXAuthority(r io.Reader) []authEntry {
	var out []authEntry
	for {
		var fam uint16
		if err := binary.Read(r, binary.BigEndian, &fam); err != nil {
			break
		}
		addr, ok := readAuthBytes(r)
		if !ok {
			break
		}
		num, ok := readAuthBytes(r)
		if !ok {
			break
		}
		name, ok := readAuthBytes(r)
		if !ok {
			break
		}
		data, ok := readAuthBytes(r)
		if !ok {
			break
		}
		out = append(out, authEntry{
			family: fam,
			addr:   string(addr),
			number: string(num),
			name:   string(name),
			data:   data,
		})
	}
	return out
}

func readAuthBytes(r io.Reader) ([]byte, bool) {
	var n uint16
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, false
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, false
	}
	return buf, true
}
