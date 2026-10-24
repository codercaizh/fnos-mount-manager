package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// fnOS stores mount aliases in a nested JSON object, commonly:
// user-id -> mount-id -> {comment, mountPoint, userName, address, ...}.
// This may contain credentials; NEVER log or return raw JSON.
const fnosMountConfigPath = "/etc/mountmgr/mount_info.json"
const maxMountConfigBytes int64 = 4 << 20
const maxMountConfigDepth = 8

func readMountConfig(filename string) ([]mountRecord, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	limited := io.LimitReader(f, maxMountConfigBytes+1)
	blob, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read mount metadata: %w", err)
	}
	if int64(len(blob)) > maxMountConfigBytes {
		return nil, errors.New("mount configuration exceeds safe size limit")
	}
	return decodeMountConfig(blob)
}

// decodeMountConfig walks nested user -> mount-ID maps, arrays and wrappers.
// The only data retained for each entry are mountRecord's explicitly declared
// JSON fields. Other fields (including stored passwords) are discarded.
func decodeMountConfig(data []byte) ([]mountRecord, error) {
	var out []mountRecord
	if err := walkMountConfig(data, "", 0, &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("no alias entries in mount configuration")
	}
	return out, nil
}

func walkMountConfig(data []byte, key string, depth int, out *[]mountRecord) error {
	if depth > maxMountConfigDepth {
		return errors.New("nested mount configuration exceeds depth limit")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err == nil && object != nil {
		// A leaf is identified by the mountPoint + comment fields. Do not
		// inspect or copy the password and other credential fields.
		if _, hasPoint := object["mountPoint"]; hasPoint {
			if _, hasAlias := object["comment"]; hasAlias {
				var r mountRecord
				if err := json.Unmarshal(data, &r); err == nil &&
					strings.TrimSpace(r.MountPoint) != "" &&
					strings.TrimSpace(r.Comment) != "" {
					if r.Name == "" {
						r.Name = key
					}
					*out = append(*out, r)
				}
			}
			return nil
		}
		for childKey, child := range object {
			switch len(child) {
			case 0:
				continue
			default:
				if child[0] != '{' && child[0] != '[' {
					continue
				}
			}
			if err := walkMountConfig(child, childKey, depth+1, out); err != nil {
				return err
			}
		}
		return nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err == nil && arr != nil {
		for _, child := range arr {
			if err := walkMountConfig(child, "", depth+1, out); err != nil {
				return err
			}
		}
		return nil
	}
	return errors.New("invalid mount metadata JSON")
}
