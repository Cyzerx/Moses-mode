"""Exercise install.sh with disposable configuration, a fake build and no services."""
import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


class LocalInstallTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        shutil.copy(Path(__file__).resolve().parents[1] / "install.sh", self.root)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.env = {"PATH": str(self.bin), "HOME": str(self.root)}
        for name in ("dirname", "mktemp", "rm", "cp", "cat", "ln", "mv", "sed", "base64", "tr", "head", "chmod", "mkdir", "rmdir", "cmp", "realpath"):
            (self.bin / name).symlink_to(shutil.which(name))
        self.config = self.root / "config.json"
        self.fake("go", """#!/bin/sh
if [ "$1" = run ]; then
    exec check-fixture
fi
if [ "$1" = build ]; then
    [ -z "$FAIL_BUILD" ] || exit 9
    while [ "$1" != -o ]; do shift; done
    cp "$FIXTURE_BINARY" "$2" && chmod 755 "$2"
fi
""")
        self.fake("check-fixture", f"""#!{os.sys.executable}
import json, os, sys
with open(os.environ['ARTEX_MOSES_MODE_CONFIG']) as f: cfg = json.load(f)
if sys.argv[1:] == ['-merge-install-db']:
    cfg.setdefault('database', {{}}).update(json.load(sys.stdin)['database'])
    print(json.dumps(cfg))
elif not any(cfg.get('database', {{}}).get(k) for k in ['dsn', 'host', 'user', 'dbname']):
    sys.exit(3)
""")
        self.env["FIXTURE_BINARY"] = str(self.fake("artex-moses-mode-fixture", """#!/bin/sh
if [ "$#" -gt 0 ]; then exec "${ARTEX_MOSES_MODE_INSTALL_TEST_BINARY:-check-fixture}" "$@"; fi
exit 0
"""))
        if os.environ.get("ARTEX_MOSES_MODE_INSTALL_TEST_BINARY"):
            self.env["ARTEX_MOSES_MODE_INSTALL_TEST_BINARY"] = os.environ["ARTEX_MOSES_MODE_INSTALL_TEST_BINARY"]
        self.fake("docker", """#!/bin/sh
echo "$@" >> docker-calls
""")

    def fake(self, name, content):
        path = self.bin / name
        path.write_text(content)
        path.chmod(0o755)
        return path

    def run_install(self, answers):
        return subprocess.run(["/bin/bash", "install.sh"], cwd=self.root, env=self.env,
                              input=answers, text=True, capture_output=True)

    def test_existing_config_preserved_on_success_and_failure(self):
        original = b'{ "custom": "keep", "skill_dir": "mine", "database": {"host": "db"} }\n'
        self.config.write_bytes(original)
        result = self.run_install("2\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.config.read_bytes(), original)
        self.assertEqual(self.config.stat().st_mode & 0o077, 0)
        self.env["FAIL_BUILD"] = "1"
        self.assertNotEqual(self.run_install("2\n").returncode, 0)
        self.assertEqual(self.config.read_bytes(), original)

    def test_invalid_file_preserved_and_no_database_started(self):
        self.config.write_bytes(b"{malformed")
        self.assertNotEqual(self.run_install("2\n").returncode, 0)
        self.assertEqual(self.config.read_bytes(), b"{malformed")
        self.assertFalse((self.root / "docker-calls").exists())

    def test_missing_compiler_preserves_file(self):
        self.config.write_bytes(b"sentinel")
        (self.bin / "go").unlink()
        self.assertNotEqual(self.run_install("2\n").returncode, 0)
        self.assertEqual(self.config.read_bytes(), b"sentinel")

    def test_complete_partial_config_preserves_unknown_fields(self):
        self.config.write_text('{"skill_dir":"custom","future":3,"database":{"future_key":true}}')
        result = self.run_install("2\n1\n\n\n\nfixture\n\n\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        data = json.loads(self.config.read_text())
        self.assertEqual(("custom", 3), (data["skill_dir"], data["future"]))
        self.assertTrue(data["database"]["future_key"])
        self.assertEqual("fixture", data["database"]["password"])

    def test_new_config_private_and_database_loopback_only(self):
        result = self.run_install("2\n2\nsynthetic-fixture\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(0o600, self.config.stat().st_mode & 0o777)
        self.assertEqual("synthetic-fixture", json.loads(self.config.read_text())["database"]["password"])
        self.assertIn("-p 127.0.0.1:5432:5432", (self.root / "docker-calls").read_text())


if __name__ == "__main__":
    unittest.main()
