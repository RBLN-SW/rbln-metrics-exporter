# Copyright 2026 Rebellions Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Regression tests for notice generation, without Go or network access."""

import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


GENERATOR = Path(__file__).with_name("gen-third-party-notices.py")


class NoticeGenerationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / "repo"
        self.root.mkdir()
        (self.root / "NOTICE").write_text("Example Product\n", encoding="utf-8")
        self.output = self.root / "THIRD_PARTY_NOTICES"
        self.cache = Path(self.temp.name) / "module-cache" / "dep"
        self.cache.mkdir(parents=True)
        (self.cache / "LICENSE").write_text(
            "Copyright Module Owner\nPermission is hereby granted, free of charge\n",
            encoding="utf-8",
        )
        fakebin = Path(self.temp.name) / "bin"
        fakebin.mkdir()
        self.go_called = Path(self.temp.name) / "go-called"
        go = fakebin / "go"
        go.write_text(
            "#!/usr/bin/env python3\n"
            "import os\n"
            "from pathlib import Path\n"
            "assert os.environ['GOOS'] == 'linux'\n"
            "assert os.environ['GOFLAGS'] == '-mod=mod'\n"
            "Path(os.environ['TEST_GO_CALLED']).touch()\n"
            "d = os.environ['TEST_MODULE_DIR']\n"
            "print('example.org/dep', 'v1.0.0', d, d, sep='\\t')\n",
            encoding="utf-8",
        )
        go.chmod(0o755)
        self.env = dict(
            os.environ,
            PATH=str(fakebin) + os.pathsep + os.environ.get("PATH", ""),
            TEST_MODULE_DIR=str(self.cache),
            TEST_GO_CALLED=str(self.go_called),
        )

    def run_generator(self, mode):
        return subprocess.run(
            [sys.executable, str(GENERATOR), "--mode", mode,
             str(self.root), str(self.output)],
            env=self.env,
            text=True,
            capture_output=True,
        )

    def create_vendor(self):
        dep = self.root / "vendor" / "example.org" / "dep"
        dep.mkdir(parents=True)
        (self.root / "vendor" / "modules.txt").write_text(
            "# example.org/dep v1.0.0\n## explicit\nexample.org/dep\n",
            encoding="utf-8",
        )
        (dep / "dep.go").write_text("package dep\n", encoding="utf-8")
        (dep / "LICENSE").write_text(
            "Copyright Vendored Owner\nPermission is hereby granted, free of charge\n",
            encoding="utf-8",
        )
        return dep

    def test_module_output_is_unchanged_when_vendor_is_added(self):
        result = self.run_generator("module")
        self.assertEqual(result.returncode, 0, result.stderr)
        before = self.output.read_bytes()
        self.assertIn(b"Copyright Module Owner", before)
        self.create_vendor()
        result = self.run_generator("module")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.output.read_bytes(), before)
        self.assertTrue(self.go_called.exists())

    def test_vendor_includes_nested_licenses_without_running_go(self):
        dep = self.create_vendor()
        nested = dep / "internal" / "third_party"
        nested.mkdir(parents=True)
        (nested / "LICENSE").write_text(
            "Copyright Nested Owner\nPermission is hereby granted, free of charge\n",
            encoding="utf-8",
        )
        result = self.run_generator("vendor")
        self.assertEqual(result.returncode, 0, result.stderr)
        contents = self.output.read_text(encoding="utf-8")
        self.assertIn("Copyright Vendored Owner", contents)
        self.assertIn("Copyright Nested Owner", contents)
        self.assertFalse(self.go_called.exists())

    def test_missing_vendor_fails_instead_of_switching_to_modules(self):
        result = self.run_generator("vendor")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("requires vendor/modules.txt", result.stderr)
        self.assertFalse(self.go_called.exists())
        self.assertFalse(self.output.exists())


if __name__ == "__main__":
    unittest.main()
