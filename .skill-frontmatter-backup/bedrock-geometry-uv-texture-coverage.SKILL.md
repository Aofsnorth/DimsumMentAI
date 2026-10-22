---
name: bedrock-geometry-uv-texture-coverage
description: Use when auditing whether a Minecraft Bedrock geometry's cube `uv` anchors actually sample usable content in the texture they point at - detecting invisible cubes/faces whose box-UV rect lands on a transparent corner, proving a texture's designed hotspot is dead code, and measuring the true sampled texel region. Covers the box-UV layout formula, verifying it empirically against a vanilla humanoid texture with a known face, per-face sampled-rect tables, union/intersection coverage, surface-area-weighted transparency, and the pixel-index vs continuous-centre half-texel convention. Triggers: "invisible shard", "cube renders as nothing", "uv samples transparent corner", "does the geometry use the texture", "core is lopsided", "texture audit for a geo", "radial texture but geometry samples the edge".
---

# Bedrock geometry box-UV vs texture coverage

Answer: **does this model actually show the texture, and does it show the part
the generator designed?** A cube can be perfectly well-formed JSON and render as
nothing, because its `uv` anchor put every face on a transparent texel.

## When to Use

- An entity/projectile in a resource pack renders partially or wholly
  invisible, or "the shard is missing".
- A generator paints a radial/central hotspot but the model looks dim and flat.
- An adversarial texture audit asks for "the uv ranges and the corresponding
  sampled region".
- You must separate *art* defects from *UV placement* defects.

## When NOT to Use

- Auditing a generator against its own PNG bytes (use
  `procedural-atlas-readonly-audit`).
- Authoring new art/generators (use `mc-procedural-png-texture-gen`).
- Particle `uv` blocks — those are a plain rect, no box unfolding.

## Key idea

A cube `"uv": [u, v]` with a `size` of `[w, h, d]` is **not** a rect at `(u,v)`.
It is a box unfolding. Only knowing the layout lets you say which pixels a face
reads.

```
up    : (u+d,     v)     size w x d
down  : (u+d+w,   v)     size w x d
east  : (u,       v+d)   size d x h      (right)
north : (u+d,     v+d)   size w x h      (front)
west  : (u+d+w,   v+d)   size d x h      (left)
south : (u+2*d+w, v+d)   size w x h      (back)
```

Bounding rect: `x ∈ [u, u+2w+2d]`, `y ∈ [v, v+d+h]`. **The union of the six
faces tiles that rect exactly** — so the bounding rect *is* the sampled area,
and any transparent texel inside it is a transparent piece of the model.

**Verify the layout empirically, do not trust memory or the docs.** Pick a
vanilla humanoid texture with an unambiguous face (zombie/piglin) and its
geometry cube at `uv [0,0]` size `[8,8,8]`. Compute per-face RGB variance in the
64x32 image; the max-variance face must come out as `north` at `(8,8)` — the
face with the eyes. If it does not, your layout is rotated and every number you
produce is wrong.

## Procedure

1. Read the geometry. For every cube record `size`, `uv`, `inflate` (absent =
   `0.01` in some mergers — treat it as 0 for UV purposes) and the parent bone.
   Note `texture_width`/`texture_height`; if the texture's real size differs the
   UV space is scaled and the rect math changes.
2. **Verify the layout** with the vanilla humanoid check above. Print it.
3. Per face, compute the exact rect, then the pixel window
   `[floor(x), ceil(x+fw)) x [floor(y), ceil(y+fh))`. For each: mean/max alpha,
   opaque fraction, and count of fully transparent texels. Print the table — it
   is the report's evidence.
4. Classify: any face at 100% alpha 0 is an **invisible face**. A face whose
   caps are invisible on a long bone reads as an open tube when it rotates.
5. Union coverage: rasterise all face rects into one boolean mask over the
   texture; report `opaque%`, `fullyTransparent%`, and the radius or distance
   range of sampled texels.
6. **Intersect with intent.** Take the generator's declared hotspot(s) (a radial
   disc band, a swatch, a ramp cell) and ask how many of those texels the UV
   union actually covers. If the union's max sampled radius is less than the
   hotspot's min radius, the hotspot is *dead code* — say so with both numbers.
   Quote the maximum alpha/level the geometry can ever display.
7. Weight by geometry, not by texel count: `sum(opaque% * faceArea) / sum(area)`.
   Texel-count fractions mislead you when faces differ in size by 10x (caps vs
   shafts).
8. For rotation, re-ask the question per bone: a bone that spins about Y sweeps
   its four side faces through the texture, so a bone with 3 invisible side
   faces is a *blank* bone, not a subtly-wrong one.
9. Report the union rect and, if it does not cover the hotspot, the intersection
   is empty — that is the headline finding.

## Pitfalls

- **Pixel index vs continuous coordinate.** Pixel `i` covers `[i, i+1)`; the
  centre of a 64px axis is continuous `32.0`, but the image's mirror axis in
  *pixel-index* space is `31.5`. Art drawn "centred at 32.0" is half a texel
  off-centre. Test both mirrors (`a[x] vs a[-x-1]` and `a[x] vs a[64-x]`) or you
  will report a systematic offset as "lopsided art" and vice versa.
- A near-zero-alpha texel is invisible *and* it may still count as paint in the
  generator's own asserts. Coverage must be measured on alpha, not on "the
  generator drew something there".
- `inflate` / `mirror` on a bone invert and grow the sampled region; a mirrored
  bone flips U and can silently mirror a face onto a transparent strip.
- Fractional cube sizes (e.g. `2.4`) make face rects non-integer; use
  `floor/ceil` windows and say so, since that over-counts by up to 1 texel.
- A texture can be perfectly symmetric and still place its content off the
  image's mirror axis. Report the *mirror axis the content is symmetric about*
  alongside the mean absolute difference, or the number is uninterpretable.
- Do not call it a "seam" when opaque content touches the border but the
  wrapped neighbour is fully transparent — that is only an asymmetric rim. A
  real seam needs opaque content on both sides of the wrap.
- Emissive materials (`entity_emissive_alpha`) commonly read texel **alpha as
  the emissive strength**, so a soft-alpha texture is the right idiom for a
  glow. Do not report "washed out" by default; check first whether the geometry
  can even reach the bright texels.
- If the pack has both a base and a `*_GLOW`/emissive palette, check which one
  the emissive asset was painted from. Painting an emissive material with the
  dim base palette is a real finding, and the spec usually says so explicitly.

## Verification

- Vanilla humanoid face check prints `north @ (8,8)` with the highest variance.
- Per-face table lists alpha stats for all six faces of every cube.
- Union coverage printed as both texel % and surface-area %, and they agree to
  within a few points.
- Hotspot intersection printed as two radius ranges (sampled vs designed) with
  the count of hotspot texels inside the union — 0 is the defect.
- Read-only: re-hash the texture after the audit and state it is unchanged; the
  audit must not re-encode the PNG.
