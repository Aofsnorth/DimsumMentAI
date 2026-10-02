// Package husbandry provides animal milking and shearing methods for the bot.
//
// Both interactions produce exactly one item and no particle: a milk bucket, or
// a ball of wool. That makes the inventory the confirmation rather than a
// fallback, and it means the old "clicked once, said yes" version was never
// more than a guess dressed as a result.
package husbandry

import (
	"context"
	"strings"
)

// MilkCow milks a nearby cow and reports whether a milk bucket actually
// appeared.
func (m *Manager) MilkCow(ctx context.Context) bool {
	bucketSlot, bucketStack, ok := m.findItemSlot(isEmptyBucket)
	if !ok {
		m.report("milk", "bucket", false, "tidak punya bucket kosong")
		return false
	}

	cow, ok := m.findNearestEntity(func(t string) bool { return t == "cow" || t == "mooshroom" })
	if !ok {
		m.report("milk", "cow", false, "tidak ada sapi di sekitar")
		return false
	}

	if err := m.bot.EquipItem(bucketSlot); err != nil {
		m.report("milk", "bucket", false, "tidak bisa memegang bucket")
		return false
	}

	before := m.inventory()
	m.interactWithEntity(cow, bucketSlot, bucketStack, 0.8, m.currentTimings())

	if !sleepCtx(ctx, m.currentTimings().Action) {
		return false
	}

	if !MilkConfirmed(before, m.inventory()) {
		m.logger.Warn("milking finished with no milk bucket in the inventory")
		m.report("milk", "cow", false, "tidak ada milk bucket")
		return false
	}
	m.report("milk", "cow", true, "")
	return true
}

// ShearSheep shears a nearby sheep and reports how much wool turned up.
func (m *Manager) ShearSheep(ctx context.Context) bool {
	shearsSlot, shearsStack, ok := m.findItemSlot(func(name string) bool {
		return strings.Contains(name, "shears")
	})
	if !ok {
		m.report("shear", "shears", false, "tidak punya gunting")
		return false
	}

	sheep, ok := m.findNearestEntity(func(t string) bool { return t == "sheep" })
	if !ok {
		m.report("shear", "sheep", false, "tidak ada domba di sekitar")
		return false
	}

	if err := m.bot.EquipItem(shearsSlot); err != nil {
		m.report("shear", "shears", false, "tidak bisa memegang gunting")
		return false
	}

	before := m.inventory()
	m.interactWithEntity(sheep, shearsSlot, shearsStack, 0.8, m.currentTimings())

	if !sleepCtx(ctx, m.currentTimings().Action) {
		return false
	}

	gained, ok := ShearConfirmed(before, m.inventory())
	if !ok {
		m.logger.Warn("shearing finished with no wool in the inventory")
		m.report("shear", "sheep", false, "tidak ada wol yang masuk inventory")
		return false
	}

	m.logger.Info("sheep shorn", "wool", gained)
	m.bot.ReportActionStatus("", newCountStatus("shear", "sheep", gained))
	return true
}
