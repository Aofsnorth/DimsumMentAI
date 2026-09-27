#!/usr/bin/env python3
"""Print every record in a ms window from a BOT_PACKET_CAPTURE JSONL file."""
import json
import sys

path = sys.argv[1]
lo = int(sys.argv[2])
hi = int(sys.argv[3])

with open(path, "r", encoding="utf-8") as f:
    for line in f:
        line = line.strip()
        if not line:
            continue
        try:
            r = json.loads(line)
        except json.JSONDecodeError:
            continue
        ms = r.get("ms")
        if ms is None or not (lo <= ms <= hi):
            continue
        if "event" in r:
            print(f"[{ms:6d}ms] EVENT {r['event']}")
            continue
        extra = ""
        if "pai" in r:
            pai = r["pai"]
            extra = (f" tick={pai['tick']} pos={tuple(round(v, 2) for v in pai['pos'])}"
                     f" flags={pai['flags']}")
        print(f"[{ms:6d}ms] {r['dir']:3s} {r['name']} ({r['bytes']}B){extra}")
