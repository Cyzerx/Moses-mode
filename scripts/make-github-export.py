#!/usr/bin/env python3
"""Create a source-only Artex-Moses-mode folder and ZIP beside the working directory.

The export uses a root allowlist so new runtime directories cannot silently
enter a future upload. It never reads the local keys, database, or backups.
"""

from __future__ import annotations

import os
import re
import shutil
import tempfile
import zipfile
from pathlib import Path


SOURCE = Path(__file__).resolve().parents[1]
DESTINATION = SOURCE.parent / f"{SOURCE.name}-github-clean"
ARCHIVE = SOURCE.parent / f"{SOURCE.name}-github-clean.zip"

ROOT_FILES = {
    ".dockerignore", ".env.example", ".gitattributes", ".gitignore", ".mailmap",
    "CHANGELOG.md", "CONTRIBUTING.md", "Dockerfile", "LICENSE", "README.md",
    "RSI.md", "SECURITY.md", "build.sh", "config.example.json", "dev.sh",
    "docker-compose.bench.yml", "docker-compose.yml", "go.mod", "go.sum",
    "install.sh", "reset-password.sh", "start.bat", "start.sh", "update.sh",
}
ROOT_DIRS = {
    ".github", "agent", "cmd", "config", "db", "docs", "egress", "enrich",
    "evidence", "extension", "guard", "intercept", "llmpool", "llmrec",
    "mcphttp", "notify", "report", "scripts", "server",
    "sidequestion", "skills", "traffic", "web",
}
SKIP_DIRS = {
    ".git", ".next", ".claude", ".idea", ".vscode", ".playwright-mcp",
    ".playwright-cli", "node_modules", "__pycache__", "dist", "out", "backups",
    "posture-runs", "benchmark-results", "data", "keys", "artex-moses-mode.local",
}
SKIP_FILES = {
    ".env", "config.json", "jwt.key", "secret.key", "scheduler_state.json",
    "next-env.d.ts", "tsconfig.tsbuildinfo", "admin-credentials.txt",
}
SKIP_SUFFIXES = {
    ".db", ".sqlite", ".pem", ".key", ".p12", ".pfx", ".log",
    ".zip", ".gz", ".pyc", ".pyo",
}
SECRET_MARKERS = (
    re.compile(rb"-----BEGIN (?:RSA |EC |OPENSSH |ENCRYPTED )?PRIVATE KEY-----"),
    re.compile(rb"\bsk-(?:proj|svcacct|ant)-[A-Za-z0-9_-]{20,}"),
    re.compile(rb"\bgh[pousr]_[A-Za-z0-9]{30,}"),
    re.compile(rb"\bAIza[0-9A-Za-z_-]{35}"),
)


def allowed(path: Path) -> bool:
    if path.is_symlink():
        return False
    if any(part in SKIP_DIRS for part in path.relative_to(SOURCE).parts):
        return False
    if path.name in SKIP_FILES or path.suffix.lower() in SKIP_SUFFIXES:
        return False
    if path.name.startswith(".env") and path.name != ".env.example":
        return False
    if path.name.endswith((".db-wal", ".db-shm", "-events.csv")):
        return False
    return True


def main() -> None:
    if DESTINATION.exists() or ARCHIVE.exists():
        raise SystemExit("Export already exists; move it before making a new one")
    with tempfile.TemporaryDirectory(prefix=".artex-moses-mode-export-", dir=SOURCE.parent) as temp:
        stage = Path(temp) / DESTINATION.name
        stage.mkdir()
        copied: list[Path] = []
        for name in sorted(ROOT_FILES):
            src = SOURCE / name
            if not src.is_file() or not allowed(src):
                raise RuntimeError(f"Expected source file is missing or excluded: {name}")
            dst = stage / name
            shutil.copy2(src, dst)
            copied.append(dst)
        for name in sorted(ROOT_DIRS):
            root = SOURCE / name
            if not root.is_dir() or root.is_symlink():
                raise RuntimeError(f"Expected source directory is missing: {name}")
            for current, dirs, files in os.walk(root, followlinks=False):
                parent = Path(current)
                dirs[:] = sorted(d for d in dirs if allowed(parent / d))
                for filename in sorted(files):
                    src = parent / filename
                    if not src.is_file() or not allowed(src):
                        continue
                    dst = stage / src.relative_to(SOURCE)
                    dst.parent.mkdir(parents=True, exist_ok=True)
                    shutil.copy2(src, dst)
                    copied.append(dst)
        for path in copied:
            if path.stat().st_size > 20 * 1024 * 1024:
                raise RuntimeError(f"Unexpected large file in export: {path.relative_to(stage)}")
            data = path.read_bytes()
            if any(marker.search(data) for marker in SECRET_MARKERS):
                raise RuntimeError(f"Possible credential in export: {path.relative_to(stage)}")

        staged_zip = Path(temp) / ARCHIVE.name
        with zipfile.ZipFile(staged_zip, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=6) as bundle:
            for path in sorted(copied):
                bundle.write(path, arcname=str(Path(DESTINATION.name) / path.relative_to(stage)))
        stage.rename(DESTINATION)
        staged_zip.rename(ARCHIVE)
    print(f"Created {DESTINATION} ({len(copied)} files)")
    print(f"Created {ARCHIVE}")


if __name__ == "__main__":
    main()
