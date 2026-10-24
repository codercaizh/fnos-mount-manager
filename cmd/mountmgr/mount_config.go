package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// fnOS's own mount manager persists remote connection details here.
// Read ONLY the named fields needed for labels: the original document may
// contain SMB credentials. Never log, return, cache, or persist its contents.
// This is a best-effort compatibility source for privileged fnOS installs.
const fnosMountConfigPath = "/etc/mountmgr/mount_info.json"
const maxMountConfigBytes int64 = 4 << 20

func readMountConfig(path string) ([]mountRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxMountConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read mount metadata: %w", err)
	}
	if int64(len(data)) > maxMountConfigBytes {
		return nil, errors.New("mount configuration exceeds safe size limit")
	}
	// No metadata or credential fields are written to disk or logs.
	return decodeMountConfig(data)
}

// decodeMountConfig accepts a JSON array, standard fnOS list containers, or
// a map keyed by mount ID. Individual records use an explicit allowlisted
// struct, so password, token, and other sensitive properties are discarded.
func decodeMountConfig(data []byte) ([]mountRecord, error) {
	var arr []mountRecord
	if err := json.Unmarshal(data, &arr); err == nil {
		return nonEmptyMountRecords(arr), nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, errors.New("invalid mount metadata")
	}
	for _, key := range []string{"mounts", "mountInfo", "mountList", "connections", "records", "list", "items", "data", "rsp"} {
		if nested, exists := obj[key]; exists {
			return decodeMountConfigContainer(nested)
		}
	}
	return decodeMountConfigMap(obj)
}

func decodeMountConfigContainer(data []byte) ([]mountRecord, error) {
	var arr []mountRecord
	if err := json.Unmarshal(data, &arr); err == nil {
		return nonEmptyMountRecords(arr), nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, errors.New("unsupported mount config container")
	}
	return decodeMountConfigMap(obj)
}

func decodeMountConfigMap(values map[string]json.RawMessage) ([]mountRecord, error) {
	out := make([]mountRecord, 0, len(values))
	for key, raw := range values {
		var r mountRecord
		if err := json.Unmarshal(raw, &r); err != nil {
			continue
		}
		if r.Name == "" {
			r.Name = key
		}
		if r.MountPoint == "" || r.Comment == "" {
			continue
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, errors.New("no mount entries in map")
	}
	return out, nil
}

func nonEmptyMountRecords(records []mountRecord) []mountRecord {
	out := make([]mountRecord, 0, len(records))
	for _, r := range records {
		if r.MountPoint != "" && r.Comment != "" {
			out = append(out, r)
		}
	}
	return out
}
