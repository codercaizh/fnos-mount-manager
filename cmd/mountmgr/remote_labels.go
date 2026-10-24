package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync/atomic"
	"time"
)

// fnOS keeps the user-entered remote mount alias in the mount manager's
// comment field. The Linux mount table does not carry this information.
// Try the authenticated fnOS API-scope socket, then semantic path conversion.
const fnosAPISocket = "/var/run/trim_open_gateway_apiscope.socket"

var fnosReqID atomic.Uint64

type mountRecord struct {
	Name       string `json:"name"`
	MountPoint string `json:"mountPoint"`
	Comment    string `json:"comment"`
	UserName   string `json:"userName"`
	Username   string `json:"username"`
	Address    string `json:"address"`
}

func fnosCall(ctx context.Context, method string, payload any) (json.RawMessage, error) {
	token := os.Getenv("TRIM_API_TOKEN")
	if token == "" {
		return nil, errors.New("fnOS API token unavailable")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 700 * time.Millisecond}).DialContext(ctx, "unix", fnosAPISocket)
	}}
	defer transport.CloseIdleConnections()
	body, err := json.Marshal(map[string]any{
		"reqId": fmt.Sprintf("fnos-mount-manager-%d", fnosReqID.Add(1)),
		"req":   method, "appName": "fnos-mount-manager", "data": payload,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/api/v1/trimapp", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Transport: transport, Timeout: 1300 * time.Millisecond}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fnOS API HTTP %d", resp.StatusCode)
	}
	var result struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return nil, err
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("fnOS API rejected %s (code %d)", method, result.Code)
	}
	return result.Data, nil
}
func decodeMountRecords(data []byte) ([]mountRecord, error) {
	var items []mountRecord
	if err := json.Unmarshal(data, &items); err == nil {
		return items, nil
	}
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, err
	}
	for _, key := range []string{"rsp", "result", "list", "items", "data"} {
		nested, ok := wrapper[key]
		if !ok {
			continue
		}
		if err := json.Unmarshal(nested, &items); err == nil {
			return items, nil
		}
	}
	return nil, errors.New("fnOS mount list response contains no records")
}
func decodedMountPoint(s string) string {
	if s == "" {
		return ""
	}
	// fnOS's legacy display path uses literal '%' as a slash separator
	// (vol02%1000-0-...), not RFC3986 percent encoding.
	if strings.HasPrefix(s, "vol02%") {
		s = strings.ReplaceAll(s, "%", "/")
	} else if v, e := url.PathUnescape(s); e == nil {
		s = v
	}
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return path.Clean(s)
}
func aliasForMount(r mountRecord) string {
	comment := strings.TrimSpace(r.Comment)
	// Without an explicit alias, prefer readable SMB source fallback instead
	// of exposing an opaque /vol02 mount point as the display label.
	if comment == "" {
		return ""
	}
	user := strings.TrimSpace(r.UserName)
	if user == "" {
		user = strings.TrimSpace(r.Username)
	}
	address := strings.TrimSpace(r.Address)
	suffix := strings.Trim(strings.Join([]string{user, address}, "@"), "@")
	if user != "" && address != "" {
		suffix = user + "@" + address
	}
	if suffix != "" {
		return comment + " - " + suffix
	}
	return comment
}
func applyMountAliases(shares []RemoteShare, records []mountRecord) {
	for i := range shares {
		expected := path.Base(shares[i].Path)
		for _, r := range records {
			// Trust the mount point when present. Never apply metadata for
			// another mount merely because its opaque ID happens to match.
			if r.MountPoint != "" {
				if decodedMountPoint(r.MountPoint) != shares[i].Path {
					continue
				}
			} else if r.Name != expected {
				continue
			}
			label := aliasForMount(r)
			if label != "" {
				shares[i].Name = label
				shares[i].LabelSource = "fnos"
			}
			break
		}
	}
}
func applySemanticLabels(shares []RemoteShare, raw []byte) {
	var result struct {
		Result []struct {
			Path         string `json:"path"`
			SemanticPath string `json:"semanticPath"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return
	}
	lookup := map[string]string{}
	for _, v := range result.Result {
		if v.Path == "" || v.SemanticPath == "" {
			continue
		}
		label := strings.TrimPrefix(strings.TrimSpace(v.SemanticPath), "远程挂载/")
		label = strings.TrimPrefix(label, "远程挂载 > ")
		if label != "" && label != v.Path {
			lookup[v.Path] = label
		}
	}
	for i := range shares {
		if shares[i].LabelSource == "fnos" {
			continue
		}
		if label := lookup[shares[i].Path]; label != "" {
			shares[i].Name = label
			shares[i].LabelSource = "semantic"
		}
	}
}

type fnosCaller func(context.Context, string, any) (json.RawMessage, error)

func enrichRemoteShares(ctx context.Context, shares []RemoteShare) {
	enrichRemoteSharesUsing(ctx, shares, fnosCall)
}

func enrichRemoteSharesUsing(ctx context.Context, shares []RemoteShare, caller fnosCaller) {
	if len(shares) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// Direct mount manager data gives the exact alias used by File Manager.
	if raw, err := caller(ctx, "appcgi.mountmgr.list", map[string]any{}); err == nil {
		if list, err := decodeMountRecords(raw); err == nil {
			applyMountAliases(shares, list)
			// On some firmwares the endpoint returns [] or mount records
			// without a comment. We must still try semantic conversion.
			resolved := true
			for _, share := range shares {
				if share.LabelSource != "fnos" {
					resolved = false
					break
				}
			}
			if resolved {
				return
			}
		}
	}
	paths := make([]string, 0, len(shares))
	for _, r := range shares {
		paths = append(paths, r.Path)
	}
	if raw, err := caller(ctx, "trim.file.convertPath", map[string]any{"path": paths, "language": "zh-CN"}); err == nil {
		applySemanticLabels(shares, raw)
	}
}

// shareLabelFromSource is a human-readable fallback when fnOS metadata cannot
// be accessed. The mount source, not the opaque vol02 ID, carries host/share.
func shareLabelFromSource(source, pathName string) string {
	s := strings.TrimPrefix(unescapeMount(source), "//")
	if strings.HasPrefix(source, "//") {
		fields := strings.Split(s, "/")
		if len(fields) >= 2 && fields[0] != "" && fields[1] != "" {
			return fields[1] + " · " + fields[0]
		}
	}
	return "SMB 共享 · " + pathName
}
