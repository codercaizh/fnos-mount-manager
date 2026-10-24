package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMountConfigParsesOfficialAliasWithoutLeakingPasswords(t *testing.T) {
	// Contents use throwaway sample credentials; no actual NAS file is accessed.
	config := `[
  {"name":"1000-0-abc","type":"smb","mountPoint":"/vol02/1000-0-abc","comment":"妈妈相册","userName":"myp","address":"10.0.2.50","password":"TOP_SECRET_TEST_VALUE"},
  {"name":"1000-0-def","type":"smb","mountPoint":"/vol02/1000-0-def","comment":"爸爸相册","userName":"caizh","address":"10.0.2.50","password":"SECOND_SECRET_VALUE"}
 ]`
	dir := t.TempDir()
	file := filepath.Join(dir, "mount_info.json")
	if err := os.WriteFile(file, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	records, err := readMountConfig(file)
	if err != nil || len(records) != 2 {
		t.Fatalf("config parse: %v %+v", err, records)
	}
	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	shares := []RemoteShare{
		{Path: "/vol02/1000-0-def", Name: "我的照片-234388861 · 10.0.2.50", LabelSource: "smb"},
		{Path: "/vol02/1000-0-abc", Name: "我的照片-243836522 · 10.0.2.50", LabelSource: "smb"},
	}
	applyMountAliases(shares, records)
	if shares[0].Name != "爸爸相册" || shares[0].Subtitle != "caizh@10.0.2.50" {
		t.Fatalf("dad album label wrong: %+v", shares[0])
	}
	if shares[1].Name != "妈妈相册" || shares[1].Subtitle != "myp@10.0.2.50" {
		t.Fatalf("mom album label wrong: %+v", shares[1])
	}
	after, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Fatal("metadata file modified")
	}
	encoded, err := json.Marshal(shares)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "SECRET_VALUE") || strings.Contains(string(encoded), "TOP_SECRET") {
		t.Fatal("mount credential leaked to response")
	}
}
func TestMountConfigContainers(t *testing.T) {
	cases := []struct{ name, raw string }{
		{"array", `[{"name":"1000-0-a","comment":"Dad","mountPoint":"/vol02/1000-0-a","password":"secret"}]`},
		{"mounts array", `{"mounts":[{"name":"1000-0-a","comment":"Dad","mountPoint":"/vol02/1000-0-a","password":"secret"}]}`},
		{"mount info array", `{"mountInfo":[{"name":"1000-0-a","comment":"Dad","mountPoint":"/vol02/1000-0-a"}]}`},
		{"keyed map", `{"1000-0-a":{"comment":"Dad","mountPoint":"/vol02/1000-0-a","password":"secret"}}`},
		{"keyed mounts", `{"mounts":{"1000-0-a":{"comment":"Dad","mountPoint":"/vol02/1000-0-a"}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			records, err := decodeMountConfig([]byte(tc.raw))
			if err != nil || len(records) != 1 || records[0].Comment != "Dad" || records[0].MountPoint != "/vol02/1000-0-a" {
				t.Fatalf("%v %+v", err, records)
			}
			if records[0].Name != "1000-0-a" {
				t.Fatalf("invalid name: %+v", records[0])
			}
		})
	}
}
func TestMountConfigRejectsOversizedAndInvalid(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mount_info.json")
	if _, err := readMountConfig(p); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file err=%v", err)
	}
	if err := os.WriteFile(p, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readMountConfig(p); err == nil {
		t.Fatal("corrupt config accepted")
	}
	if err := os.WriteFile(p, []byte(strings.Repeat("x", int(maxMountConfigBytes)+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readMountConfig(p); err == nil {
		t.Fatal("oversized config accepted")
	}
}
func TestNativeConfigPrecedenceAndAPIFallback(t *testing.T) {
	shares := []RemoteShare{
		{Path: "/vol02/1000-0-a", Name: "raw a", LabelSource: "smb"},
		{Path: "/vol02/1000-0-b", Name: "raw b", LabelSource: "smb"},
	}
	fileCalls := 0
	local := func() ([]mountRecord, error) {
		fileCalls++
		return []mountRecord{{Name: "1000-0-a", MountPoint: "/vol02/1000-0-a", Comment: "妈妈相册", UserName: "myp", Address: "10.0.2.50"}}, nil
	}
	apiCalls := []string{}
	api := func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		apiCalls = append(apiCalls, method)
		if method == "appcgi.mountmgr.list" {
			return []byte(`[{"name":"1000-0-a","mountPoint":"/vol02/1000-0-a","comment":"错误别名"},{"name":"1000-0-b","mountPoint":"/vol02/1000-0-b","comment":"爸爸相册","userName":"caizh","address":"10.0.2.50"}]`), nil
		}
		return nil, errors.New("should not need semantic fallback")
	}
	enrichRemoteSharesWithLocal(context.Background(), shares, api, local)
	if fileCalls != 1 || len(apiCalls) != 1 {
		t.Fatalf("unexpected lookups file=%d api=%v", fileCalls, apiCalls)
	}
	if shares[0].Name != "妈妈相册" || shares[1].Name != "爸爸相册" {
		t.Fatalf("incorrect precedence %+v", shares)
	}
	if shares[0].Subtitle != "myp@10.0.2.50" || shares[1].Subtitle != "caizh@10.0.2.50" {
		t.Fatalf("incorrect descriptions %+v", shares)
	}
}

func TestRealFnOSMountInfoNestedUserAndMountIDs(t *testing.T) {
	// Verified shape: first JSON object is user ID, second is mount ID,
	// and the record leaf carries comment and mountPoint.
	const fixture = `{
   "1000": {
      "1000-0-abc": {"type":"smb","comment":"妈妈相册","mountPoint":"/vol02/1000-0-abc","userName":"myp","address":"10.0.2.50","password":"DO_NOT_EXPOSE_1"},
      "1000-0-def": {"type":"smb","comment":"爸爸相册","mountPoint":"/vol02/1000-0-def","userName":"caizh","address":"10.0.2.50","password":"DO_NOT_EXPOSE_2"}
   },
   "1001": {
      "1001-0-ghi": {"comment":"额外共享","mountPoint":"/vol02/1001-0-ghi","userName":"user","address":"nas.lan","password":"DO_NOT_EXPOSE_3"}
   }
 }`
	dir := t.TempDir()
	p := filepath.Join(dir, "mount_info.json")
	if err := os.WriteFile(p, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := readMountConfig(p)
	if err != nil || len(entries) != 3 {
		t.Fatalf("nested fnOS metadata parse failed: %v, %+v", err, entries)
	}
	shares := []RemoteShare{
		{Path: "/vol02/1000-0-def", Name: "opaque-def", LabelSource: "smb"},
		{Path: "/vol02/1000-0-abc", Name: "opaque-abc", LabelSource: "smb"},
		{Path: "/vol02/1001-0-ghi", Name: "opaque-ghi", LabelSource: "smb"},
	}
	applyMountAliases(shares, entries)
	want := [][2]string{
		{"爸爸相册", "caizh@10.0.2.50"},
		{"妈妈相册", "myp@10.0.2.50"},
		{"额外共享", "user@nas.lan"},
	}
	for i, share := range shares {
		if share.Name != want[i][0] || share.Subtitle != want[i][1] || share.LabelSource != "fnos" {
			t.Errorf("unexpected mounted share %d: %+v", i, share)
		}
	}
	blob, _ := json.Marshal(shares)
	if strings.Contains(string(blob), "DO_NOT_EXPOSE") {
		t.Fatal("credentials leaked from nested configuration")
	}
}

func TestNestedConfigIgnoresNonMountObjectsAndDepthLimit(t *testing.T) {
	const config = `{
   "settings":{"comment":"ignore","password":"never log","value":{"enabled":true}},
   "users":{"1000":{"m1":{"mountPoint":"/vol02/1000-0-valid","comment":"Valid"},
                   "m2":{"mountPoint":"/vol02/1000-0-blank","comment":""}}},
   "other":{"foo":"bar"}
 }`
	records, err := decodeMountConfig([]byte(config))
	if err != nil || len(records) != 1 || records[0].Comment != "Valid" {
		t.Fatalf("nested filter failed: %+v %v", records, err)
	}
	nested := `{"x":`
	for i := 0; i < maxMountConfigDepth+1; i++ {
		nested += `{"x":`
	}
	nested += `{}`
	for i := 0; i < maxMountConfigDepth+2; i++ {
		nested += `}`
	}
	if _, err := decodeMountConfig([]byte(nested)); err == nil {
		t.Fatal("overly deep config accepted")
	}
}
