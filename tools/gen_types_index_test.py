"""Tests for the error-catalog checks in gen_types_index.py."""
import pathlib
import shutil
import subprocess
import sys
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).resolve().parent / 'gen_types_index.py'

CATALOG = """### Requirement: Error catalog

| Code | Kind | HTTP | Source |
|---|---|---|---|
| `gohan.foo` | Permanent | 401 | `ErrFoo` |

#### Scenario: every sentinel has a code
ID: `errors.every-sentinel-has-code`
- WHEN `spec:types` runs
- THEN every sentinel and typed error declared in a spec appears in the catalog

### Requirement: Blobs

#### Scenario: blob stored by reference
ID: `messages.blob-stored-by-ref`
- WHEN `Send` carries a 3 MB `Image`
- THEN `SessionLog` holds the block with `Blob{Ref, SHA256, Bytes}`
"""

STORES = """```go
var ErrFoo = errors.New("gohan: foo")
```
"""

MESSAGES = ""


def make_tree(tmp, stores=STORES, messages=MESSAGES, catalog=CATALOG):
    for rel in ('openspec/specs/messages', 'openspec/specs/stores', 'tools', 'docs/design'):
        (tmp / rel).mkdir(parents=True, exist_ok=True)
    (tmp / 'openspec/specs/stores/spec.md').write_text(stores)
    (tmp / 'openspec/specs/messages/spec.md').write_text(messages + catalog)
    (tmp / 'docs/design/scenarios.md').write_text('')
    shutil.copy(SCRIPT, tmp / 'tools/gen_types_index.py')


def run_tree(tmp):
    return subprocess.run(
        [sys.executable, str(tmp / 'tools/gen_types_index.py')],
        capture_output=True, text=True, timeout=60,
    )


class GenTypesIndexTest(unittest.TestCase):
    def setUp(self):
        self.tmp = pathlib.Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)

    def test_healthy_fixture_passes(self):
        make_tree(self.tmp)
        r = run_tree(self.tmp)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)

    def test_typed_error_without_row_fails(self):
        make_tree(self.tmp, stores=STORES + "```go\ntype LeakError struct{}\n```\n")
        r = run_tree(self.tmp)
        self.assertEqual(r.returncode, 1)
        self.assertIn('missing-rows:', r.stdout)
        self.assertIn('LeakError', r.stdout)

    def test_row_without_source_fails(self):
        catalog = CATALOG.replace(
            '| `gohan.foo` | Permanent | 401 | `ErrFoo` |',
            '| `gohan.foo` | Permanent | 401 |  |',
        )
        make_tree(self.tmp, catalog=catalog)
        r = run_tree(self.tmp)
        self.assertEqual(r.returncode, 1)
        self.assertIn('empty-source:', r.stdout)
        self.assertIn('gohan.foo', r.stdout)

    def test_source_naming_undeclared_error_fails(self):
        catalog = CATALOG.replace('`ErrFoo`', '`ErrGhost`')
        make_tree(self.tmp, catalog=catalog)
        r = run_tree(self.tmp)
        self.assertEqual(r.returncode, 1)
        self.assertIn('undeclared-source:', r.stdout)
        self.assertIn('ErrGhost', r.stdout)


if __name__ == '__main__':
    unittest.main()
