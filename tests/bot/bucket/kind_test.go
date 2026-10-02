package bucket_test

import (
	"testing"

	"bedrock-ai/internal/bot/bucket"
)

func block(name string) bucket.FillSource { return bucket.BlockSource(name) }
func mob(name string) bucket.FillSource   { return bucket.EntitySource(name) }

// --- Classifying buckets ----------------------------------------------

func TestClassifyBucketKnowsEveryKind(t *testing.T) {
	t.Parallel()

	cases := map[string]bucket.BucketKind{
		"minecraft:bucket":               bucket.BucketEmpty,
		"minecraft:water_bucket":         bucket.BucketWater,
		"minecraft:lava_bucket":          bucket.BucketLava,
		"minecraft:powder_snow_bucket":   bucket.BucketPowderSnow,
		"minecraft:cod_bucket":           bucket.BucketFish,
		"minecraft:salmon_bucket":        bucket.BucketFish,
		"minecraft:tropical_fish_bucket": bucket.BucketFish,
		"minecraft:pufferfish_bucket":    bucket.BucketFish,
		"minecraft:axolotl_bucket":       bucket.BucketAxolotl,
		"minecraft:milk_bucket":          bucket.BucketMilk,
		"bucket":                         bucket.BucketEmpty,
		"powder snow bucket":             bucket.BucketPowderSnow,
		"Minecraft:Water_Bucket":         bucket.BucketWater,
	}
	for name, want := range cases {
		if got := bucket.ClassifyBucket(name); got != want {
			t.Errorf("ClassifyBucket(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestANonBucketIsNotABucket(t *testing.T) {
	t.Parallel()

	// The old milking code used "contains bucket" and would happily pick up
	// any of these.
	for _, name := range []string{"", "minecraft:hoe", "minecraft:oak_log", "minecraft:water", "bucket_pot"} {
		if got := bucket.ClassifyBucket(name); got != bucket.BucketUnknown {
			t.Errorf("ClassifyBucket(%q) = %v, want BucketUnknown", name, got)
		}
	}
}

func TestOnlySomeBucketsAreFilled(t *testing.T) {
	t.Parallel()

	if bucket.BucketEmpty.IsFilled() {
		t.Error("an empty bucket was reported as filled")
	}
	if !bucket.BucketWater.IsFilled() {
		t.Error("a water bucket was reported as empty")
	}
	// Milk is a bucket and it is full, but you cannot pour it out, and a
	// routine that treats it like water will try.
	if !bucket.BucketMilk.IsFilled() {
		t.Error("a milk bucket was reported as empty")
	}
	if bucket.BucketMilk.CanBePoured() {
		t.Error("a milk bucket was reported as pourable")
	}
}

// --- What a bucket can be filled from ---------------------------------

func TestWaterLavaAndPowderSnowAreFillable(t *testing.T) {
	t.Parallel()

	cases := map[string]bucket.BucketKind{
		"minecraft:water":          bucket.BucketWater,
		"minecraft:flowing_water":  bucket.BucketWater,
		"minecraft:bubble_column":  bucket.BucketWater,
		"minecraft:standing_water": bucket.BucketWater,
		"minecraft:lava":           bucket.BucketLava,
		"minecraft:flowing_lava":   bucket.BucketLava,
		"minecraft:powder_snow":    bucket.BucketPowderSnow,
	}
	for name, want := range cases {
		got, ok := bucket.BucketFillPlan(block(name))
		if !ok {
			t.Errorf("BucketFillPlan(%q) found nothing to fill", name)
			continue
		}
		if got != want {
			t.Errorf("BucketFillPlan(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestFishAndAxolotlAreCaughtInBuckets(t *testing.T) {
	t.Parallel()

	cases := map[string]bucket.BucketKind{
		"cod":           bucket.BucketFish,
		"salmon":        bucket.BucketFish,
		"tropical_fish": bucket.BucketFish,
		"pufferfish":    bucket.BucketFish,
		"axolotl":       bucket.BucketAxolotl,
		"tadpole":       bucket.BucketFish,
	}
	for name, want := range cases {
		got, ok := bucket.BucketFillPlan(mob(name))
		if !ok {
			t.Errorf("BucketFillPlan(entity %q) found nothing to fill", name)
			continue
		}
		if got != want {
			t.Errorf("BucketFillPlan(entity %q) = %v, want %v", name, got, want)
		}
	}
}

func TestAnUnfillableTargetIsRejected(t *testing.T) {
	t.Parallel()

	// This list is the honest half of the acceptance criteria: a bucket
	// cannot be filled from a wall, a cow, or the sky.
	for _, src := range []bucket.FillSource{
		block("minecraft:stone"),
		block("minecraft:air"),
		block("minecraft:oak_log"),
		block("minecraft:dirt"),
		block("minecraft:grass"),
		block("minecraft:cauldron"),
		mob("minecraft:cow"),
		mob("minecraft:player"),
		mob(""),
		block(""),
	} {
		if got, ok := bucket.BucketFillPlan(src); ok {
			t.Errorf("BucketFillPlan(%s) = %v, want no plan", src.Label(), got)
		}
	}
}

// --- Where a bucket can be emptied ------------------------------------

func TestFluidBucketsPourOntoAirAndIntoCauldrons(t *testing.T) {
	t.Parallel()

	for _, kind := range []bucket.BucketKind{bucket.BucketWater, bucket.BucketLava, bucket.BucketPowderSnow} {
		for _, target := range []string{"minecraft:air", "minecraft:cave_air", "minecraft:cauldron", "minecraft:short_grass"} {
			if ok, reason := bucket.BucketEmptyPlan(kind, block(target)); !ok {
				t.Errorf("%v refused to pour onto %s: %s", kind, target, reason)
			}
		}
		if ok, _ := bucket.BucketEmptyPlan(kind, block("minecraft:stone")); ok {
			t.Errorf("%v poured onto solid stone; the water would just flow away", kind)
		}
	}
}

func TestLivingBucketsOnlyGoBackIntoWater(t *testing.T) {
	t.Parallel()

	for _, kind := range []bucket.BucketKind{bucket.BucketFish, bucket.BucketAxolotl} {
		if ok, _ := bucket.BucketEmptyPlan(kind, block("minecraft:water")); !ok {
			t.Errorf("%v refused to go back into water", kind)
		}
		if ok, _ := bucket.BucketEmptyPlan(kind, block("minecraft:cauldron")); !ok {
			t.Errorf("%v refused to go into a cauldron", kind)
		}
		if ok, reason := bucket.BucketEmptyPlan(kind, block("minecraft:air")); ok {
			t.Errorf("%v was poured onto dry land (%s); it would suffocate", kind, reason)
		}
	}
}

func TestAnEmptyBucketCannotBePoured(t *testing.T) {
	t.Parallel()

	if ok, _ := bucket.BucketEmptyPlan(bucket.BucketEmpty, block("minecraft:air")); ok {
		t.Error("an empty bucket was poured")
	}
	if ok, _ := bucket.BucketEmptyPlan(bucket.BucketUnknown, block("minecraft:air")); ok {
		t.Error("an unrecognised bucket was poured")
	}
}
