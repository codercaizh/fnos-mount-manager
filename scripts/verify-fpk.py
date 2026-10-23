#!/usr/bin/env python3
"""Validate official fnpack products for architecture, structure and checksum."""
import io
import re
import struct
import tarfile
import hashlib
from pathlib import Path
root = Path(__file__).resolve().parent.parent
manifest = (root / "packaging/fpk/manifest").read_text(encoding="utf-8")
match = re.search(r'^version="([^"]+)"', manifest, re.M)
assert match, "missing version"
version = match.group(1)
for suffix, platform, machine in (("arm64", "arm", 183), ("x86_64", "x86", 62)):
    path = root / "dist" / f"fnos-mount-manager-{version}-{suffix}.fpk"
    assert path.is_file(), path
    with tarfile.open(path, "r:gz") as outer:
        members = set(outer.getnames())
        expected = {"app.tgz", "manifest", "config/privilege", "config/resource",
                    "cmd/main", "cmd/install_init", "cmd/upgrade_init", "ICON.PNG", "ICON_256.PNG"}
        assert expected <= members, expected - members
        fields = {}
        for line in outer.extractfile("manifest").read().decode().splitlines():
            if "=" in line:
                k, v = line.split("=", 1)
                fields[k.strip()] = v.strip().strip('"')
        assert fields.get("version") == version, fields
        assert fields.get("platform") == platform, fields
        with tarfile.open(fileobj=io.BytesIO(outer.extractfile("app.tgz").read()), mode="r:gz") as inner:
            files = set(inner.getnames())
            assert {"server/mountmgr", "ui/config", "ui/images/icon_256.png"} <= files
            header = inner.extractfile("server/mountmgr").read(64)
            assert header.startswith(b"\x7fELF")
            assert struct.unpack("<H", header[18:20])[0] == machine, "wrong ELF machine"
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    expected_digest = (path.parent / (path.name + ".sha256")).read_text().split()[0]
    assert digest == expected_digest
    print(f"Verified: {path.name}, platform={platform}, ELF={machine}, sha256={digest}")
