#!/usr/bin/env python3
"""Compare two `primus run --collection` outputs endpoint by endpoint and flag regressions.

    compare_runs.py baseline.json current.json [--p95-tolerance 0.10] [--min-ms 20] [--error-tolerance 0.005]

An endpoint regresses when its verdict went from passed to anything else, its p95 grew by more than
--p95-tolerance (a fraction) AND by more than --min-ms milliseconds, or its error rate grew by more than
--error-tolerance (absolute fraction). Prints a Markdown table; exits 1 if anything regressed, 2 on bad input.
Standard library only.
"""
import argparse
import json
import sys


def load(path):
    try:
        with open(path, encoding="utf-8") as f:
            data = json.load(f)
    except (OSError, ValueError) as e:
        sys.exit(f"compare_runs: cannot read {path}: {e}")
    if "results" not in data:
        sys.exit(f"compare_runs: {path} is not a `primus run --collection` output (no results)")
    return {r["request_id"]: r for r in data["results"]}


def metrics(item):
    run = item.get("run") or {}
    s = run.get("summary") or {}
    return item.get("status"), s.get("p95_millis"), s.get("p99_millis"), s.get("error_rate")


def fmt_rate(v):
    return "-" if v is None else f"{v * 100:.2f}%"


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("baseline")
    ap.add_argument("current")
    ap.add_argument("--p95-tolerance", type=float, default=0.10)
    ap.add_argument("--min-ms", type=float, default=20)
    ap.add_argument("--error-tolerance", type=float, default=0.005)
    a = ap.parse_args()

    base, cur = load(a.baseline), load(a.current)
    rows, regressed = [], 0
    for rid in sorted(set(base) | set(cur), key=lambda r: (cur.get(r) or base.get(r))["name"]):
        b, c = base.get(rid), cur.get(rid)
        name = (c or b)["name"]
        if b is None or c is None:
            rows.append((name, "-" if b is None else metrics(b)[0], "-" if c is None else metrics(c)[0],
                         "-", "-", "new endpoint" if b is None else "missing now"))
            continue
        bs, bp95, _, berr = metrics(b)
        cs, cp95, _, cerr = metrics(c)
        reasons = []
        if bs == "passed" and cs != "passed":
            reasons.append(f"verdict {bs} -> {cs}")
        if bp95 is not None and cp95 is not None and cp95 > bp95 * (1 + a.p95_tolerance) and cp95 - bp95 > a.min_ms:
            reasons.append(f"p95 +{cp95 - bp95}ms")
        if berr is not None and cerr is not None and cerr - berr > a.error_tolerance:
            reasons.append(f"errors +{(cerr - berr) * 100:.2f}pt")
        if reasons:
            regressed += 1
        p95 = f"{bp95} -> {cp95} ms" if bp95 is not None and cp95 is not None else "-"
        rows.append((name, bs, cs, p95, f"{fmt_rate(berr)} -> {fmt_rate(cerr)}",
                     "REGRESSED: " + ", ".join(reasons) if reasons else "ok"))

    print(f"Tolerances: p95 +{a.p95_tolerance * 100:.0f}% and +{a.min_ms:g} ms, error rate +{a.error_tolerance * 100:.2f} pt\n")
    print("| Endpoint | Baseline | Current | p95 | Error rate | Result |")
    print("|---|---|---|---|---|---|")
    for r in rows:
        print("| " + " | ".join(str(x) for x in r) + " |")
    print(f"\n{regressed} of {len(rows)} endpoints regressed.")
    return 1 if regressed else 0


if __name__ == "__main__":
    sys.exit(main())
