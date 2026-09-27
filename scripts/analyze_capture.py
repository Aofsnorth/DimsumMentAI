#!/usr/bin/env python3
"""Summarize a BOT_PACKET_CAPTURE JSONL capture.

Reports, per direction, the packet-type histogram and a timeline of the first
N seconds, then finds the last S2C packet and lists every C2S packet type sent
before and after the silence started.
"""
import json
import sys
from collections import Counter

path = sys.argv[1]
window_ms = int(sys.argv[2]) if len(sys.argv) > 2 else 10000

records = []
with open(path, "r", encoding="utf-8") as f:
    for line in f:
        line = line.strip()
        if not line:
            continue
        try:
            records.append(json.loads(line))
        except json.JSONDecodeError:
            pass

pkts = [r for r in records if "dir" in r and r.get("dir") != "?"]
if not pkts:
    print("no packets captured")
    sys.exit(1)

# Normalize: capture spans multiple sessions (reconnects). Find session starts.
starts = [i for i, r in enumerate(records) if r.get("event") == "capture-start"]
session_bounds = []
if starts:
    for j, s in enumerate(starts):
        end = starts[j + 1] if j + 1 < len(starts) else len(records)
        session_bounds.append((s, end))
else:
    session_bounds = [(0, len(records))]

for si, (s, e) in enumerate(session_bounds):
    ses = [r for r in records[s:e] if "dir" in r and r["dir"] != "?"]
    if not ses:
        continue
    c2s = [r for r in ses if r["dir"] == "C2S"]
    s2c = [r for r in ses if r["dir"] == "S2C"]
    print(f"=== SESSION {si + 1}: {len(c2s)} C2S, {len(s2c)} S2C ===")

    # Silence start: last S2C timestamp.
    if s2c:
        last_s2c = s2c[-1]["ms"]
        print(f"last S2C at ms={last_s2c} ({s2c[-1]['name']})")
        after = [r for r in c2s if r["ms"] > last_s2c]
        print(f"C2S packets after silence began: {len(after)}")
        hist_after = Counter(r["name"] for r in after)
        for name, n in hist_after.most_common():
            print(f"   {name:45s} {n}")

    print("--- C2S timeline (first window) ---")
    for r in c2s:
        if r["ms"] > window_ms:
            break
        pai = r.get("pai")
        if pai and r["name"] == "packet.PlayerAuthInput":
            pos = ",".join(f"{v:.2f}" for v in pai["pos"])
            flags = ",".join(pai["flags"]) or "-"
            print(f"  [{r['ms']:6d}ms] {r['name']} tick={pai['tick']} pos=({pos}) "
                  f"moveVec={pai['moveVec']} delta={pai['delta']} yaw={pai['yaw']:.1f} "
                  f"mode={pai['inputMode']}/{pai['playMode']}/{pai['interact']} flags={flags}")
        else:
            print(f"  [{r['ms']:6d}ms] {r['name']} ({r['bytes']}B) hex={r['hex'][:40]}")

    print("--- S2C timeline (first 3s) ---")
    for shown, r in enumerate(s2c):
        if r["ms"] > 3000 or shown > 60:
            break
        print(f"  [{r['ms']:6d}ms] {r['name']} ({r['bytes']}B)")

    print()
