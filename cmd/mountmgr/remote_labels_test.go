package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestFnOSMountAliasesMatchExactPath(t *testing.T) {
	shares := []RemoteShare{
		{Path: "/vol02/1000-0-abc", Name: "Photos · 192.168.1.10"},
		{Path: "/vol02/1000-0-def", Name: "Backups · 192.168.1.20"},
	}
	records := []mountRecord{
		{Name: "1000-0-abc", MountPoint: "/vol02/1000-0-other", Comment: "错误别名不可串号"},
		{Name: "1000-0-def", MountPoint: "/vol02/1000-0-def", Comment: "家庭备份", UserName: "mom", Address: "192.168.1.20"},
		{Name: "1000-0-abc", MountPoint: "/vol02/1000-0-abc", Comment: "家庭相册", UserName: "dad", Address: "192.168.1.10"},
		{Name: "1000-0-abc-evil", MountPoint: "/vol02/1000-0-abc-evil", Comment: "不应使用"},
	}
	applyMountAliases(shares, records)
	if shares[0].Name != "家庭相册" || shares[0].Subtitle != "dad@192.168.1.10" {
		t.Errorf("wrong album alias: %s", shares[0].Name)
	}
	if shares[1].Name != "家庭备份" || shares[1].Subtitle != "mom@192.168.1.20" {
		t.Errorf("wrong backup alias: %s", shares[1].Name)
	}
}

func TestFnOSMountAliasFallbacks(t *testing.T) {
	cases := []struct{ source, expected string }{
		{"//192.168.1.101/Photos", "Photos · 192.168.1.101"},
		{"//fileserver/My Share", "My Share · fileserver"},
		{"//[fe80::1]/Family", "Family · [fe80::1]"},
		{"", "SMB 共享 · 1000-0-abc"},
	}
	for _, v := range cases {
		got := shareLabelFromSource(v.source, "1000-0-abc")
		if got != v.expected {
			t.Errorf("%q => %q, want %q", v.source, got, v.expected)
		}
	}
	if name := aliasForMount(mountRecord{MountPoint: "/vol02/1000-0-abc", Comment: ""}); name != "" {
		t.Errorf("missing custom comment should not expose raw path, got %q", name)
	}
	if alias := aliasForMount(mountRecord{Comment: "我的 NAS", Address: "192.168.1.1"}); alias != "我的 NAS - 192.168.1.1" {
		t.Errorf("host only suffix: %q", alias)
	}
}

func TestDecodeMountRecordsNestedResponse(t *testing.T) {
	for _, raw := range []string{
		`[{"name":"1000-0-abc","comment":"照片","mountPoint":"/vol02/1000-0-abc"}]`,
		`{"rsp":[{"name":"1000-0-abc","comment":"照片"}]}`,
		`{"data":[{"name":"1000-0-abc","comment":"照片"}]}`,
	} {
		out, err := decodeMountRecords([]byte(raw))
		if err != nil || len(out) != 1 || out[0].Comment != "照片" {
			t.Fatalf("decode %s: %+v err=%v", raw, out, err)
		}
	}
	for _, raw := range []string{`{}`, `{"code":0}`, "not-json"} {
		if out, err := decodeMountRecords([]byte(raw)); err == nil {
			t.Errorf("accepted invalid response %q %+v", raw, out)
		}
	}
	if path := decodedMountPoint("vol02%1000-0-abc"); path != "/vol02/1000-0-abc" {
		t.Errorf("decoded unexpected path %q", path)
	}
}

func TestFnOSSemanticLabels(t *testing.T) {
	shares := []RemoteShare{{Path: "/vol02/1000-0-abc", Name: "raw-abc"}, {Path: "/vol02/1000-0-def", Name: "raw-def"}}
	payload := map[string]any{"result": []map[string]string{
		{"path": "/vol02/1000-0-abc", "semanticPath": "远程挂载/家庭照片 - dad@NAS"},
		{"path": "/vol02/1000-0-unknown", "semanticPath": "远程挂载/其他"},
	}}
	b, _ := json.Marshal(payload)
	applySemanticLabels(shares, b)
	if shares[0].Name != "家庭照片 - dad@NAS" {
		t.Errorf("semantic label=%q", shares[0].Name)
	}
	if shares[1].Name != "raw-def" {
		t.Errorf("unrelated mount altered: %q", shares[1].Name)
	}
}
func TestMountInfoExtractsCIFSSource(t *testing.T) {
	raw := "42 1 0:50 / /vol02/1000-0-abc rw,relatime - cifs //192.168.1.10/Photos rw,vers=3.1.1\n"
	result := parseMounts(raw)
	if len(result) != 1 || result[0].Source != "//192.168.1.10/Photos" {
		t.Fatalf("%+v", result)
	}
	shares := remoteSharesFromMounts(result)
	if len(shares) != 1 || !strings.Contains(shares[0].Name, "Photos") {
		t.Fatalf("expected user-friendly UNC label, got %+v", shares)
	}
}

func TestEnrichRemoteSharesContinuesAfterEmptyAPIList(t *testing.T) {
	calls := []string{}
	shares := []RemoteShare{{Path: "/vol02/1000-0-abc", Name: "Photos · 10.0.2.50", LabelSource: "smb"}}
	caller := func(_ context.Context, method string, data any) (json.RawMessage, error) {
		calls = append(calls, method)
		if method == "appcgi.mountmgr.list" {
			return json.RawMessage(`[]`), nil
		}
		if method == "trim.file.convertPath" {
			return json.RawMessage(`{"result":[{"path":"/vol02/1000-0-abc","semanticPath":"远程挂载/相册备份"}]}`), nil
		}
		t.Fatalf("Unexpected method: %s", method)
		return nil, nil
	}
	enrichRemoteSharesUsing(context.Background(), shares, caller)
	if !reflect.DeepEqual(calls, []string{"appcgi.mountmgr.list", "trim.file.convertPath"}) {
		t.Fatalf("fallback skipped: %v", calls)
	}
	if shares[0].Name != "相册备份" || shares[0].LabelSource != "semantic" {
		t.Fatalf("fallback alias not adopted: %+v", shares[0])
	}
}
func TestEnrichRemoteSharesPartialMetadata(t *testing.T) {
	shares := []RemoteShare{
		{Path: "/vol02/1000-0-a", Name: "fallback-a", LabelSource: "smb"},
		{Path: "/vol02/1000-0-b", Name: "fallback-b", LabelSource: "smb"},
	}
	caller := func(_ context.Context, method string, data any) (json.RawMessage, error) {
		if method == "appcgi.mountmgr.list" {
			return json.RawMessage(`[{"name":"1000-0-a","mountPoint":"/vol02/1000-0-a","comment":"飞牛别名"}]`), nil
		}
		if method == "trim.file.convertPath" {
			return json.RawMessage(`{"result":[{"path":"/vol02/1000-0-a","semanticPath":"远程挂载/不应覆盖"},{"path":"/vol02/1000-0-b","semanticPath":"远程挂载/另外一条"}]}`), nil
		}
		return nil, errors.New("unknown method")
	}
	enrichRemoteSharesUsing(context.Background(), shares, caller)
	if shares[0].Name != "飞牛别名" || shares[0].LabelSource != "fnos" {
		t.Fatalf("native metadata replaced: %+v", shares[0])
	}
	if shares[1].Name != "另外一条" || shares[1].LabelSource != "semantic" {
		t.Fatalf("partial fallback failed: %+v", shares[1])
	}
}
