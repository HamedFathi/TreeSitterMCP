"""Package a built TreeSitterMCP checkout into a self-contained release archive.

Run after scripts/install.sh (or install.ps1) has produced bin/, the native FFI
library, and the prepared parser cache:

    python scripts/package.py --version 1.0.0 --platform linux-x86_64 --out dist

Produces dist/TreeSitterMCP-<version>-<platform>.tar.gz (.zip on Windows) and a
matching .sha256 file. bin/ holds the executables plus the native FFI library,
and the server finds runtime/parsers next to bin/ on its own.
"""
import argparse
import hashlib
import os
from pathlib import Path
import shutil
import stat
import sys
import tarfile
import tempfile
import zipfile

BASE = Path(__file__).resolve().parents[1]
PLATFORMS = {"linux-x86_64", "linux-arm64", "macos-x86_64", "macos-arm64", "windows-x86_64", "windows-arm64"}
DOCS = ["LICENSE", "NOTICE", "README.md", "CHANGELOG.md", "SECURITY.md", "docs/LANGUAGES.md",
        "third_party/GRAMMAR_NOTICES.md", "third_party/grammar-provenance.json", "third_party/SOURCE.md",
        "third_party/languagepack/LICENSE", "examples/claude_desktop_config.json", "examples/claude_desktop_windows.json"]


def copy(src: Path, dst: Path, executable: bool = False) -> None:
    if not src.exists():
        sys.exit(f"missing required file: {src}")
    dst.parent.mkdir(parents=True, exist_ok=True)
    if src.is_dir():
        shutil.copytree(src, dst, symlinks=True)
    else:
        shutil.copy2(src, dst)
    if executable:
        dst.chmod(dst.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--version", required=True)
    ap.add_argument("--platform", required=True, choices=sorted(PLATFORMS))
    ap.add_argument("--out", default="dist")
    a = ap.parse_args()

    windows = a.platform.startswith("windows")
    exe = ".exe" if windows else ""
    name = f"TreeSitterMCP-{a.version}-{a.platform}"
    out = Path(a.out).resolve()
    out.mkdir(parents=True, exist_ok=True)

    with tempfile.TemporaryDirectory() as tmp:
        stage = Path(tmp) / name
        for tool in ("TreeSitterMCP", "packctl"):
            copy(BASE / "bin" / f"{tool}{exe}", stage / "bin" / f"{tool}{exe}", executable=True)
        libs = list((BASE / "bin").glob("*ts_pack_core_ffi*"))
        if not libs:
            sys.exit(f"native library not found in {BASE / 'bin'}; run the build script first")
        for lib in libs:
            copy(lib, stage / "bin" / lib.name)
        parsers = BASE / "runtime/parsers"
        if not parsers.is_dir() or not any(parsers.rglob("*")):
            sys.exit("runtime/parsers is empty; run the install script first")
        copy(parsers, stage / "runtime/parsers")
        for doc in DOCS:
            copy(BASE / doc, stage / doc)

        if windows:
            archive = out / f"{name}.zip"
            with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as z:
                for p in sorted(stage.rglob("*")):
                    z.write(p, p.relative_to(stage.parent).as_posix())
        else:
            archive = out / f"{name}.tar.gz"
            with tarfile.open(archive, "w:gz", compresslevel=9) as t:
                t.add(stage, arcname=name)

    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    (out / f"{archive.name}.sha256").write_text(f"{digest}  {archive.name}\n", encoding="utf-8")
    print(f"{archive} ({archive.stat().st_size / 1e6:.1f} MB) sha256={digest}")
    if "GITHUB_OUTPUT" in os.environ:
        with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as f:
            f.write(f"archive={archive}\n")


if __name__ == "__main__":
    main()
