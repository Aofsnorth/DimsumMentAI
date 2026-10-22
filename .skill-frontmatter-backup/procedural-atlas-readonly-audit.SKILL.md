---
name: procedural-atlas-readonly-audit
description: Use when auditing a procedural texture/sprite-sheet generator against the PNG it claims to produce, under a strict read-only mandate (read pixels, never rewrite the artifact). Covers proving the shipped PNG is exactly the generator's output by rebuilding it in RAM and re-encoding, running the real generator end-to-end with ROOT/OUT redirected to a temp dir so its asserts fire without touching the file, per-texel cell cross-checks, index-order vs mapping-order consistency, and colour-distinctness/ramp measurement. Triggers: "audit the generator", "does the PNG match the generator", "read-only texture audit", "atlas cell mismatch", "verify idempotence without rewriting".
---

# Read-only audit: procedural generator vs shipped PNG

Answer "is the shipped image really what the generator makes, and is the
generator correct?" without writing a single byte into the project.

## When to Use

- A frozen/frozen-ish artifact (atlas, sprite sheet, icon) must be verified
  against the script that generated it.
- The mandate is explicitly read-only: inspect pixels, do not rewrite images.
- You need a per-cell / per-region table of generator intent vs actual pixels.
- You suspect an index-order, mapping, or palette-substitution defect.

## When NOT to Use

- You own the generator and are free to edit it (just run it).
- Pure image-quality review with no generator (use a preview/QA pass instead).
- Authoring a new generator (see `mc-procedural-png-texture-gen`).

## Key idea

**Never run the generator with its real output path first.** Two read-only
techniques together are stronger than `python gen.py && git diff`:

1. **RAM reconstruction** — import the generator as a module, call its
   per-cell painting function, rebuild the whole sheet in a `PIL.Image`, encode
   it to a `BytesIO` with the *same* save options, and compare sha256 to the
   shipped file. Byte-equal ⇒ the PNG is provably the generator's output.
2. **Sandboxed real run** — point the module's own `ROOT`/`OUT` globals at a
   temp dir and call `main()`. Every `assert` and the post-save re-open check
   still execute (so you test the real code path), but the project file is
   untouched. `OUT.relative_to(ROOT)` in a print means **both** globals must
   move together, or the run dies at a cosmetic line after the useful work.

## Procedure

1. Read the generator fully and extract, before touching the image: sheet size,
   cell pitch/grid, the key→cell map, the painting algorithm, and the exact
   order in which origins are assigned. Note whether the paint loop iterates
   the palette dict (order cosmetic) or an index table (order load-bearing).
2. Record hashes first: `sha256(PNG)` and `size/mode`. Everything after this
   must be provable against that hash.
3. Import the generator (`sys.path.insert(0, gen.parent)`), rebuild in RAM,
   re-encode, compare hash. Then per cell: iterate `key -> origin` and compare
   `paint_cell(key)` to `im.getpixel()` for all pitch² texels; count mismatches
   and assert alpha on painted texels. **png == generator per texel** is the
   load-bearing result of the whole audit.
4. Sandboxed run for the asserts + idempotence: `g.ROOT=tmp; g.OUT=tmp/"x.png";
   g.main()`; compare the produced hash to the shipped hash. Record exit code.
5. Order check: build the mapping two ways (`idx -> (idx % cols, idx // cols)`
   vs `cell_origin(k)`), print side by side, flag disagreeing indices. Then
   resolve **every** consumer reference (geometry `uv`, frame table, sprite
   index) through the same mapping and report any reference landing on an
   undeclared/fallback/transparent cell. Grep all consumers, not one.
6. Coverage: list palette keys with no declared cell (these fall back and make
   `cell_origin(key)` raise `KeyError`), painted-but-unreferenced cells, and
   unwritten cells. An unwritten cell is only a defect if something samples it.
7. Overflow: assert `paint_cell` returns exactly `pitch × pitch` for every key,
   then measure on the PNG — opaque texels outside every painted rect must be
   0, transparent texels inside painted rects must be 0. Also probe the 1-texel
   seam ring across each boundary.
8. Colour semantics: mean RGB per cell; monotonicity and channel separation for
   any declared ramp; pairwise mean-L1 distance for keys the spec calls
   distinct. For a ramp, also report the MAX adjacent-pixel RGB delta at
   `alpha > 128` (a hard edge reveals a reversed/broken `smoothstep`). Verify
   colours lie on the declared palette segments (point-to-line distance) —
   correct colours in the wrong ORDER is the common trap. Report the closest
   pairs with numbers — "looks close" is not a
   finding. Watch for a *substituted* base (e.g. a glow/emissive variant painted
   as the base), which silently makes a whole documented colour unreachable.
9. Determinism: `grep -nEi "random|time|datetime|clock|urandom|uuid|seed|
   monotonic|perf_counter"` plus the import list. A hash-derived
   `hash01(*args)` (sha256 → float) is deterministic; only flag real entropy
   sources.
10. Leave all project files byte-identical; re-hash the artifact at the end and
    state that the before/after hashes match.

## Pitfalls

- Don't `git diff` instead of hashing when the repo has no `.git` (common in
  these packs) — check `git rev-parse` first.
- `Image.getdata()` is deprecated (Pillow 12, removed in 14); use `getpixel`
  or `tobytes()`.
- Hash-identity across machines is *not* portable: `save(optimize=True)` bytes
  depend on the Pillow/zlib build. Prove identity on the audited interpreter and
  say so; do not claim cross-machine byte stability.
- A defensive `assert len(seen) == len(keys)` in the generator proves the grid
  is exhausted consistently, but it will **not** catch a palette key that has no
  declared slot — that key silently takes a fallback cell.
- Do not hand-compute a texel to "verify" a cell; your arithmetic is more
  likely wrong than the code. Compare against the generator's own function, and
  use hand-math only to interpret a sampled value.
- Mean L1 distance is a coarse proxy for perceptual distinctness. Report the
  metric you used and its limits rather than asserting "identical".
- **Reversed `smoothstep` edges silently invert a colour ramp.**
  `lerp(a, b, smoothstep(e0, e1, x))` with `e0 > e1` is a legal monotone-
  *decreasing* map, so no assert fires, but the colour runs backwards and then
  JUMPS at the next branch threshold. Symptom: two adjacent opaque pixels with
  a huge RGB delta at near-constant alpha. Detect by measuring the max
  adjacent-pixel Euclidean RGB distance restricted to `alpha > 128` (smooth
  ramp ~0-20; a hard ring >100) and by sampling a radial/vertical line and
  asserting each channel is monotone from core to edge. Fix is to swap the
  edge pair, e.g. `smoothstep(0.72, 0.97, x)`, not `smoothstep(0.97, 0.72, x)`.
- Initialise bbox accumulators at +inf/-inf or from the first sample; a
  `min(99, x)` seed silently clamps every result ≥ 99 and looks like a real
  anomaly.

## Verification

- `sha256(PNG)` before == after (artifact untouched).
- RAM-rebuild hash == shipped hash; per-cell mismatch count == 0.
- Sandboxed `main()` exit code 0 and its output hash == shipped hash.
- Every consumer reference resolves to a declared, painted, opaque cell.
- Report explicitly lists what is UNVERIFIABLE statically (in-game look,
  engine sampling behaviour, cross-workstream expectations).
