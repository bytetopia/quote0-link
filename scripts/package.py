import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import tarfile
import tempfile
import urllib.request
import zipfile


ROOT = Path(__file__).resolve().parents[1]
DIST = ROOT / "dist"
VERSION = "v5.3.0"
ARCHIVES = {
    "macos-arm64": ("tar.gz", "78369f963f1454ecc1b5516119e43aa5aa5cc1afeaca467458cbbeef02675dd4"),
    "macos-amd64": ("tar.gz", "ea33d15db3cc1f7aec1e6af0ed327ec13c82f139f442a100eca76d5b29094be9"),
    "windows-amd64": ("zip", "c86e30586e559f0ae5b41a828f21b4c69a7fc11a194e09391f5a2e31952b2471"),
}
SEGMENTS = {
    0x0: ("bootloader/bootloader.bin", 0x8000),
    0x8000: ("partition_table/partition-table.bin", 0x1000),
    0x10000: ("quote0-link.bin", 0x100000),
}
APP_DESCRIPTOR_OFFSET = 24 + 8
APP_DESCRIPTOR_MAGIC = b"\x32\x54\xcd\xab"
APP_VERSION_OFFSET = APP_DESCRIPTOR_OFFSET + 16
APP_VERSION_SIZE = 32


def digest(path):
    checksum = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            checksum.update(block)
    return checksum.hexdigest()


def fetch(target):
    suffix, expected = ARCHIVES[target]
    archive = DIST / ".cache" / f"esptool-{VERSION}-{target}.{suffix}"
    archive.parent.mkdir(parents=True, exist_ok=True)
    url = f"https://github.com/espressif/esptool/releases/download/{VERSION}/{archive.name}"
    if not archive.is_file() or digest(archive) != expected:
        with tempfile.NamedTemporaryFile(dir=archive.parent, delete=False) as file:
            temporary = Path(file.name)
        try:
            with urllib.request.urlopen(url, timeout=60) as response, temporary.open("wb") as file:
                shutil.copyfileobj(response, file)
            if digest(temporary) != expected:
                raise ValueError(f"SHA-256 mismatch: {archive.name}")
            temporary.replace(archive)
        finally:
            temporary.unlink(missing_ok=True)
    return archive


def package_tool(target, resources):
    archive = fetch(target)
    binary = "esptool.exe" if target.startswith("windows") else "esptool"
    resources.mkdir(parents=True, exist_ok=True)
    names = {binary: binary, "LICENSE": "esptool-LICENSE", "README.md": "esptool-README.md"}
    if archive.suffix == ".zip":
        with zipfile.ZipFile(archive) as source:
            for name, output in names.items():
                (resources / output).write_bytes(source.read(f"esptool-{target}/{name}"))
    else:
        with tarfile.open(archive, "r:gz") as source:
            for name, output in names.items():
                with source.extractfile(f"esptool-{target}/{name}") as member:
                    (resources / output).write_bytes(member.read())
        (resources / binary).chmod(0o755)
    (resources / "THIRD-PARTY.txt").write_text(
        f"Espressif esptool {VERSION}, official unmodified standalone executable.\n"
        f"Release and source: https://github.com/espressif/esptool/releases/tag/{VERSION}\n"
        f"Source archive: https://github.com/espressif/esptool/archive/refs/tags/{VERSION}.tar.gz\n"
        "The firmware uses the UC8251D driver from the MindReset Quote/0 hardware resources.\n"
        "Driver source: https://github.com/MindReset/dot_open_platform/tree/"
        "c86603987ba1632b5e2b89fc4704fbf1b007def4\n",
        encoding="utf-8",
    )


def package_firmware(build, resources):
    mapping = {}
    for line in (build / "flash_args").read_text(encoding="utf-8").splitlines():
        if line.startswith("0x"):
            address, path = line.split(maxsplit=1)
            mapping[int(address, 16)] = path
    expected = {address: path for address, (path, _) in SEGMENTS.items()}
    if mapping != expected:
        raise ValueError("ESP-IDF flash layout differs from the supported standard variant")
    release_version = (ROOT / "firmware" / "version.txt").read_text(encoding="utf-8").strip()
    project = json.loads((build / "project_description.json").read_text(encoding="utf-8"))
    if not re.fullmatch(r"(?:0|[1-9][0-9]{0,8})(?:\.(?:0|[1-9][0-9]{0,8})){2}", release_version):
        raise ValueError("Invalid firmware version; expected X.Y.Z")
    if project["project_version"] != release_version:
        raise ValueError(f"Firmware build is not version {release_version}; rebuild firmware before packaging")
    app_image = (build / "quote0-link.bin").read_bytes()
    image_version = app_image[APP_VERSION_OFFSET:APP_VERSION_OFFSET + APP_VERSION_SIZE].split(b"\0", 1)[0]
    if (app_image[APP_DESCRIPTOR_OFFSET:APP_DESCRIPTOR_OFFSET + 4] != APP_DESCRIPTOR_MAGIC
            or image_version != release_version.encode("ascii")):
        raise ValueError(f"Compiled firmware image reports a version other than {release_version}")
    if b"Q0LINK 1 152 296\n" not in app_image or b"Q0VERSION\0" not in app_image:
        raise ValueError("Not a versioned Quote0 Link firmware build")
    target = resources / "firmware" / "standard"
    target.mkdir(parents=True, exist_ok=True)
    hashes = {}
    for path, max_size in SEGMENTS.values():
        source = build / path
        if not 0 < source.stat().st_size <= max_size:
            raise ValueError(f"Unexpected segment length: {source}")
        shutil.copyfile(source, target / source.name)
        hashes[source.name] = digest(target / source.name)
    (target / "sha256.json").write_text(json.dumps(hashes, indent=2) + "\n", encoding="utf-8")
    (target / "version.txt").write_text(release_version + "\n", encoding="utf-8")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("target", choices=["mac", "windows"])
    parser.add_argument("--firmware-build", required=True, type=Path)
    options = parser.parse_args()
    if options.target == "mac":
        arch = {"arm64": "arm64", "x86_64": "amd64"}[platform.machine()]
        bundle = DIST / "Quote0 Link.app"
        resources = bundle / "Contents" / "Resources" / "maintenance"
        target = f"macos-{arch}"
    else:
        bundle = DIST / "Quote0 Link Windows"
        bundle.mkdir(parents=True, exist_ok=True)
        shutil.copy2(DIST / "Quote0 Link.exe", bundle)
        resources = bundle / "maintenance"
        target = "windows-amd64"
    package_firmware(options.firmware_build, resources)
    package_tool(target, resources)
    output = DIST / f"Quote0-Link-{target}.zip"
    if options.target == "mac":
        subprocess.run(["codesign", "--force", "--sign", "-", str(bundle)], check=True)
        output.unlink(missing_ok=True)
        subprocess.run(["ditto", "-c", "-k", "--keepParent", str(bundle), str(output)], check=True)
    else:
        with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED) as archive:
            for path in sorted(bundle.rglob("*")):
                if path.is_file():
                    archive.write(path, path.relative_to(DIST))
    print(os.fspath(output))


if __name__ == "__main__":
    main()
