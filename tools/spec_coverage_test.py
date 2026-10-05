#!/usr/bin/env python3
"""Tests for spec_coverage.py."""

import io
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent / "spec_coverage.py"

REGISTRY = {
    "flow.plain-invoke": {"capability": "flow", "origin": "R1", "title": "plain invoke"},
    "flow.backend-swap": {"capability": "flow", "origin": "R1", "title": "backend swap", "deferred_to": "M1"},
    "flow.extra": {"capability": "flow", "origin": "R1", "title": "extra", "deferred_to": "M2"},
}


def ev(action, test=None):
    e = {"Action": action}
    if test:
        e["Test"] = test
    return e


def ran(test):
    """A subtest that ran and completed successfully."""
    return [ev("run", test), ev("pass", test)]


def ndjson(events):
    return "".join(json.dumps(e) + "\n" for e in events)


def run_script(registry_path, args, stdin_data=""):
    proc = subprocess.run(
        [sys.executable, str(SCRIPT), "--registry", str(registry_path)] + args,
        input=stdin_data, capture_output=True, text=True, timeout=60,
    )
    return proc


class CoverageTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.registry = Path(self.dir.name) / "scenarios.json"
        self.registry.write_text(json.dumps(REGISTRY))

    def stream(self, events, args):
        return run_script(self.registry, args, ndjson(events))

    def test_nested_subtest_resolves_scenario(self):
        out = self.stream(ran("TestFlow/flow.plain-invoke"), [])
        self.assertEqual(out.returncode, 0)
        self.assertIn("covered=1", out.stdout)

    def test_multiple_module_streams_aggregate(self):
        out = self.stream(
            ran("TestFlow/flow.plain-invoke")
            + ran("TestAdapter/flow.backend-swap")
            + ran("TestExample/flow.plain-invoke"),
            ["--gate", "M0.5"],
        )
        self.assertEqual(out.returncode, 0)
        self.assertIn("subtests=2", out.stdout)
        self.assertIn("covered=2", out.stdout)

    def test_gate_m0_5_blocks_uncovered_in_scope(self):
        out = self.stream([], ["--gate", "M0.5"])
        self.assertEqual(out.returncode, 1)
        self.assertIn("missing_in_scope=1", out.stdout)

    def test_failing_producer_fails_pipeline(self):
        proc = subprocess.run(
            [
                "bash",
                "-o",
                "pipefail",
                "-c",
                "set -e; { printf ''; exit 7; } | "
                f"{sys.executable} {SCRIPT} --registry {self.registry}",
            ],
            text=True,
            capture_output=True,
        )
        self.assertEqual(proc.returncode, 7)

    def test_unregistered_id_shaped_name_fails(self):
        out = self.stream(ran("TestFlow/flow.bogus-id"), [])
        self.assertEqual(out.returncode, 1)
        self.assertIn("unregistered", out.stdout)

    def test_non_id_shaped_subtest_ignored(self):
        out = self.stream(ran("TestFlow/subtest_case"), [])
        self.assertEqual(out.returncode, 0)
        self.assertIn("subtests=1", out.stdout)
        self.assertNotIn("unregistered  ", out.stdout)

    def test_skipped_subtest_does_not_cover(self):
        out = self.stream(
            [ev("run", "TestFlow/flow.plain-invoke"), ev("skip", "TestFlow/flow.plain-invoke")], []
        )
        self.assertEqual(out.returncode, 0)
        self.assertIn("covered=0", out.stdout)
        self.assertIn("missing=3", out.stdout)

    def test_failed_subtest_does_not_cover(self):
        out = self.stream(
            [ev("run", "TestFlow/flow.plain-invoke"), ev("fail", "TestFlow/flow.plain-invoke")], []
        )
        self.assertEqual(out.returncode, 0)
        self.assertIn("covered=0", out.stdout)

    def test_run_without_terminal_event_does_not_cover(self):
        out = self.stream([ev("run", "TestFlow/flow.plain-invoke")], [])
        self.assertEqual(out.returncode, 0)
        self.assertIn("covered=0", out.stdout)

    def test_failed_subtest_not_reported_unregistered(self):
        out = self.stream([ev("run", "TestFlow/flow.bogus-id"), ev("fail", "TestFlow/flow.bogus-id")], [])
        self.assertEqual(out.returncode, 0)
        self.assertNotIn("unregistered  ", out.stdout)

    def test_gate_m0_fails_when_uncovered_then_passes(self):
        out = self.stream([], ["--gate", "M0"])
        self.assertEqual(out.returncode, 1)
        out = self.stream(ran("TestFlow/flow.plain-invoke"), ["--gate", "M0"])
        self.assertEqual(out.returncode, 0)

    def test_deferred_scenario_does_not_block_earlier_gate(self):
        out = self.stream([], ["--gate", "M0"])
        self.assertEqual(out.returncode, 1)
        self.assertIn("missing_in_scope=1", out.stdout)
        self.assertNotIn("unregistered  ", out.stdout)

    def test_empty_stdin_report_mode_ok(self):
        out = run_script(self.registry, [], "")
        self.assertEqual(out.returncode, 0)
        self.assertIn("registered=3", out.stdout)
        self.assertIn("missing=3", out.stdout)

    def test_unknown_deferred_to_exits_2(self):
        bad = dict(REGISTRY)
        bad["flow.bad"] = {"capability": "flow", "origin": "R1", "title": "bad", "deferred_to": "M9"}
        reg = Path(self.dir.name) / "bad-def.json"
        reg.write_text(json.dumps(bad))
        out = run_script(reg, ["--gate", "M0"])
        self.assertEqual(out.returncode, 2)
        self.assertIn("flow.bad", out.stderr)
        self.assertIn("M9", out.stderr)

    def test_malformed_registry_exits_2(self):
        bad = Path(self.dir.name) / "bad.json"
        bad.write_text("{not json")
        out = run_script(bad, [], "")
        self.assertEqual(out.returncode, 2)

    def test_unknown_milestone_exits_2(self):
        out = self.stream([], ["--gate", "M9"])
        self.assertEqual(out.returncode, 2)

    def test_test_json_file_read(self):
        tf = Path(self.dir.name) / "test.json"
        tf.write_text(ndjson(ran("TestFlow/flow.plain-invoke")))
        out = run_script(self.registry, ["--test-json", str(tf)], "should not read stdin")
        self.assertEqual(out.returncode, 0)
        self.assertIn("covered=1", out.stdout)


if __name__ == "__main__":
    unittest.main()
