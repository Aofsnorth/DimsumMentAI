// Package husbandry provides animal milking and shearing methods for the bot.
package husbandry

import (
	"context"
	"strings"

	"bedrock-ai/internal/event"
)

// MilkCow milks a nearby cow
func (m *Manager) MilkCow(ctx context.Context) bool {
	bucketSlot, bucketStack, ok := m.findItemSlot(isEmptyBucket)
	if !ok {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "milk",
			Item:    "bucket",
			Count:   0,
			Success: false,
			Error:   "gak punya bucket kosong",
		})
		return false
	}

	cow, ok := m.findNearestEntity(func(t string) bool { return t == "cow" || t == "mooshroom" })
	if !ok {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "milk",
			Item:    "cow",
			Count:   0,
			Success: false,
			Error:   "gak ada sapi",
		})
		return false
	}

	if err := m.bot.EquipItem(bucketSlot); err != nil {
		return false
	}

	m.interactWithEntity(cow, bucketSlot, bucketStack, 0.8)

	m.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "milk",
		Item:    "cow",
		Count:   1,
		Success: true,
	})
	return true
}

// ShearSheep shears a nearby sheep
func (m *Manager) ShearSheep(ctx context.Context) bool {
	shearsSlot, shearsStack, ok := m.findItemSlot(func(name string) bool { return strings.Contains(name, "shears") })
	if !ok {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "shear",
			Item:    "shears",
			Count:   0,
			Success: false,
			Error:   "gak punya gunting",
		})
		return false
	}

	sheep, ok := m.findNearestEntity(func(t string) bool { return t == "sheep" })
	if !ok {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "shear",
			Item:    "sheep",
			Count:   0,
			Success: false,
			Error:   "gak ada domba",
		})
		return false
	}

	if err := m.bot.EquipItem(shearsSlot); err != nil {
		return false
	}

	m.interactWithEntity(sheep, shearsSlot, shearsStack, 0.8)

	m.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "shear",
		Item:    "sheep",
		Count:   1,
		Success: true,
	})
	return true
}
