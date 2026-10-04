#!/usr/bin/env python3
"""M0 coverage gate: check go test -json subtests against the scenario registry."""

import argparse
import json
import sys
from pathlib import Path

RANKS = {"M0": 0, "M0.5": 1, "M1": 2, "M2": 3, "M3": 4, "M4": 5}
DEFAULT_REGISTRY = Path(__file__).resolve().parent.parent / "openspec" / "scenarios.json"


def load_registry(path):
    try:
        with open(path, "r", encoding="utf-8") as f:
            data = json.load(f)
    except (OSError, json.JSONDecodeError) as e:
        raise UsageError(f"cannot read registry {path}: {e}")
    if not isinstance(data, dict):
        raise UsageError(f"registry {path} is not a JSON object")
    return data


def parse_test_events(stream):
    for line in stream:
        line = line.strip()
        if not line:
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError as e:
            raise UsageError(f"malformed JSON in test stream: {e}")
        if not isinstance(event, dict) or event.get("Action") != "run":
            continue
        test = event.get("Test")
        if isinstance(test, str):
            yield test


def discovered_subtests(tests):
    """Return {test function: [last segments]} and the set of segments seen."""
    funcs = {}
    segments = set()
    for full in tests:
        func, _, _ = full.partition("/")
        funcs.setdefault(func, [])
        last = full.rsplit("/", 1)[-1]
        funcs[func].append(last)
        segments.add(last)
    return funcs, segments


def id_shaped(name):
    parts = name.split(".")
    return len(parts) == 2 and all(p and "." not in p for p in parts)


def gate_rank(gate):
    if gate not in RANKS:
        raise UsageError(f"unknown milestone {gate!r}")
    return RANKS[gate]


def scenario_rank(sid, entry):
    dt = entry.get("deferred_to")
    if dt is None:
        return 0
    if dt not in RANKS:
        raise UsageError(f"scenario {sid} has unknown deferred_to {dt!r}")
    return RANKS[dt]


def main(argv=None):
    try:
        return run(argv)
    except UsageError as e:
        print(f"error: {e}", file=sys.stderr)
        return 2


class UsageError(Exception):
    pass


def run(argv):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--registry", default=str(DEFAULT_REGISTRY))
    parser.add_argument("--gate", choices=None, default=None)
    parser.add_argument("--test-json", default=None)
    args = parser.parse_args(argv)

    if args.gate is not None:
        gate_rank(args.gate)  # validate before opening files

    registry = load_registry(args.registry)

    if args.test_json is not None:
        try:
            stream = open(args.test_json, "r", encoding="utf-8")
        except OSError as e:
            raise UsageError(f"cannot read test json {args.test_json}: {e}")
        with stream:
            tests = list(parse_test_events(stream))
    else:
        tests = list(parse_test_events(sys.stdin))

    funcs, segments = discovered_subtests(tests)

    unregistered = []
    for func in sorted(funcs):
        for seg in funcs[func]:
            if id_shaped(seg) and seg not in registry:
                unregistered.append((seg, func))

    registered = set(registry)
    covered = registered & segments
    missing = sorted(registered - segments)

    rank = gate_rank(args.gate) if args.gate else None
    missing_in_scope = []
    if args.gate:
        for sid in missing:
            if scenario_rank(sid, registry[sid]) <= rank:
                missing_in_scope.append(sid)

    for sid in missing:
        entry = registry[sid]
        print(f"missing  {sid}  ({entry.get('capability', '')}, deferred_to {entry.get('deferred_to', 'M0')})")
    for sid, func in sorted(unregistered):
        print(f"unregistered  {sid}  ({func})")

    summary = (
        f"summary registered={len(registered)} subtests={len(segments)} "
        f"covered={len(covered)} missing={len(missing)} "
        f"missing_in_scope={len(missing_in_scope)} unregistered={len(unregistered)}"
    )
    print(summary)

    if unregistered:
        return 1
    if args.gate and missing_in_scope:
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
