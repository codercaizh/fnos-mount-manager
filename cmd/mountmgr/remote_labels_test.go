package main

import (
	"encoding/json"
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
	if shares[0].Name != "家庭相册 - dad@192.168.1.10" {
		t.Errorf("wrong album alias: %s", shares[0].Name)
	}
	if shares[1].Name != "家庭备份 - mom@192.168.1.20" {
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
