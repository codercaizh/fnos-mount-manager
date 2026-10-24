package main

import (
	"testing"
)

func TestRemoteSharesFromMounts(t *testing.T) {
	input := []MountInfo{
		{Point: "/vol02/1000-0-bbb", FSType: "cifs"},
		{Point: "/vol02/1000-0-aaa", FSType: "smb3"},
		{Point: "/vol02/1000-0-aaa", FSType: "smb3"},   // deduplicated
		{Point: "/vol02/1000-0-bbb/a", FSType: "cifs"}, // nested share folder
		{Point: "/vol02/1000-0-ccc", FSType: "ext4"},
		{Point: "/vol02/irrelevant", FSType: "cifs"},
		{Point: "/vol1/1000/share", FSType: "cifs"},
	}
	got := remoteSharesFromMounts(input)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Path != "/vol02/1000-0-aaa" || got[1].Path != "/vol02/1000-0-bbb" {
		t.Fatalf("wrong order/paths: %+v", got)
	}
	if got[0].Name != "SMB 共享 · 1000-0-aaa" {
		t.Errorf("bad label %s", got[0].Name)
	}
}

func TestSMBRootSourceReady(t *testing.T) {
	root := "/vol02/1000-0-abcdef"
	ms := []MountInfo{{Point: root, FSType: "cifs", ID: "42"}}
	if !remotePath(root) || !sourceAllowed(root) {
		t.Fatal("SMB root should be allowed as source")
	}
	if !remoteReady(root, ms) {
		t.Fatal("mounted SMB root must be ready without subfolder")
	}
	if !remoteReady(root+"/Photos", ms) {
		t.Fatal("nested SMB source must remain supported")
	}
	if remoteReady("/vol02/1000-0-other", ms) {
		t.Fatal("unmounted SMB root should be offline")
	}
}
