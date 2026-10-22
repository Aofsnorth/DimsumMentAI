---
name: mc-procedural-png-texture-gen
description: Use when generating 2D Minecraft mod assets (FX textures, particles, GUI overlays, mod icon) procedurally with Python + Pillow instead of image editors. Covers deterministic re-runnable generator scripts (fixed seeds), supersample-draw + LANCZOS downscale for 2-3px anti-aliased edges on jagged shapes, additive-glow-friendly RGBA conventions (transparent background, white-hot cores, soft falloffs), seamless tileable value-noise fBm, per-pixel composition driven by "L" masks read via tobytes() (getdata is deprecated in Pillow 12), contact-sheet previews, per-file sha256 printing, and PIL re-open verification. Triggers: "generate textures for a mod", "procedural PNG assets", "rift/glow/spark/dot textures", "deterministic texture generator", "power-of-two RGBA textures".
---

# Deterministic procedural PNG textures (Pillow) for MC mods

Produce palette-driven, additive-glow-friendly textures with a re-runnable
script whose output is byte-stable on the same Python/Pillow/zlib build.

## When to Use

- A mod needs FX/particle/GUI/icon textures and no image editor or external
  art pipeline is available ("everything procedural, no internet").
- Aesthetic direction is given as a palette + style (e.g. neon rift, embers,
  sparkles) and you must ship matching assets.
- Byte-identical re-runs and hash reporting are required for review.

## When NOT to Use

- Sourcing or converting existing artist assets (no generation involved).
- One-off pixel edits to an existing PNG (patch pixels directly instead).
- Runtime/GPU-generated effects that must live in shaders, not PNG files.

## Procedure

1. Recon the project: target resource paths, existing conventions, Python +
   Pillow availability (`python -c "import PIL; print(PIL.__version__)"`).
   Fix a single global `SEED`; give each texture its own
   `random.Random(SEED + k)` so adding textures never shifts others.
2. Put the generator in `<mod>/tools/gen_textures.py`, resolving paths from
   `__file__` (`HERE -> MOD -> src/main/resources/assets/<modid>`), and
   `os.makedirs(..., exist_ok=True)` for every output directory.
3. Draw conventions that keep quality high:
   - Hard shapes: draw at 2x (icons 4x) on an `"L"` mask, then
     `GaussianBlur(1.2-1.5)` + `resize(..., LANCZOS)` -> 2-3 px AA edges.
   - Composite per-pixel: `render(size, fn)` writes `px[x, y] = fn(x, y)`.
   - Read finished masks with `mask.tobytes()`; index is `y * W + x` for
     `"L"` (8-bit). Do not use `getdata()` (deprecated; removal in Pillow 14).
   - Color: `ramp(t, [(pos, (r,g,b)), ...])` piecewise-linear helper; drive
     `t` by distance from a core line/spine, use `exp(-(d/sigma)**2)` for
     glows and `smoothstep(e0, e1, x)` for falloffs.
   - Additive-friendly: alpha = intensity, alpha 0 at background, bright
     near-white cores -> saturated mid -> deep edge; keep bodies of
     "normal-blend" textures (e.g. a dark sigil) the exception and say so.
4. Seamless noise: lattice octaves of 4/8/16/32/64 cells (each divides the
   power-of-two texture size) sampled with wrapped bilinear value noise ->
   exactly tileable fBm. Alpha = 255, grayscale replicated into RGB.
5. Report + verify in the script: after each save, print
   `path size sha256`; then reopen every file and assert RGBA + expected
   size. Build a contact sheet (dark checkerboard, labels via
   `ImageFont.load_default(size=...)`); keep labels on their own line so
   they cannot overflow cells.
6. Prove determinism: hash all outputs, `subprocess.run([sys.executable,
   gen_script])`, hash again, compare -> expect all byte-identical.
7. Visual QA: read the preview PNG back as an image; fix obvious defects
   (blocky noise, blobby line joints, visible square at glow edge, label
   overflow). Then write a report: table (path, size, purpose), exact
   re-run command, sha256 list, caveats (toolchain pins bytes; blend mode
   hints; tileability; vignette draw-last).

## Pitfalls

- `Image.getdata()` is deprecated in Pillow 12 (removed in 14). Use
  `tobytes()`; for `"L"`, `data[y * W + x]`; for byte-regex rewrites use
  binary read/write and CRLF-aware patterns (`(\r\n){3,}`, not `\n{3,}`).
- Polyline width steps leave gaps or blobs: seal vertices with
  `ellipse` radius = width/2, or `joint="curve"`; never radius = width.
- A radial glow looks like a square if alpha is still non-negligible at
  the image edge: end the falloff early, e.g. `smoothstep(0.02, 0.92, r)`.
- Determinism survives refactors only if RNG call order and float
  expressions stay identical (`v * (1.0/n)` vs `v / n` can differ in the
  last bit). When compacting a verified script, keep expressions verbatim
  and re-check hashes -- identical hashes prove the rewrite was neutral.
- Preview scaling: use LANCZOS for both min/mag; NEAREST makes soft glows
  look like blocky grids.
- Sizes must be power-of-two; MC atlases mip poorly otherwise.

## Verification

- Generator self-check: reopen each output, assert `size` and
  `mode == "RGBA"` with bands `("R","G","B","A")`.
- Independent check (fresh process): one-liner re-open loop over all files,
  assert mode/size, and print a sha256 table for the report.
- Determinism: second full run + hash diff -> "all N files byte-identical".
- Human-visible check: view the contact sheet image; iterate if a cell
  looks broken.

## Example (canonical implementation)

`emotesword/tools/gen_textures.py` (TLauncher workspace): 13 textures +
contact sheet, raw-jagged sigil via `blade_poly` (jittered tapered polygon),
core streak `exp(-(dv/4)^2)` along a wandering spine, seamless fBm, rune
ring drawn as blurred-mask + sharp-mask pairs, icon at 4x with additive
stars. Reuse it as the template for new mod texture sets.
