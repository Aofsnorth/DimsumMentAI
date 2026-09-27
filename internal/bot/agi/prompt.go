package agi

import (
	"fmt"
	"time"
)

// buildDecisionPrompt asks the model what to do with itself, and — just as
// importantly — tells it that doing nothing is a valid answer.
//
// The restraint clauses are the whole design. A model handed an empty schedule
// and told "act like a player" will always find something to do, and the result
// is a bot that never stops moving, which is the single most obvious tell that
// it is not a person. Saying "you may do nothing" and "you do not need to
// justify it" is what buys the stillness that makes the activity read as
// intentional.
func (r *Runner) buildDecisionPrompt(snap Snapshot) (systemPrompt, prompt string) {
	b := r.b

	b.Mu.Lock()
	botName := b.Name
	b.Mu.Unlock()

	hp, hunger, coords := b.GetStatusDetails()
	botStatusText := fmt.Sprintf("HP: %d/20, Hunger: %d/20", hp, hunger)

	systemPrompt = b.AiClient.BuildSystemPrompt(
		botName,
		coords+" ("+botStatusText+")",
		"",
		b.GetHeldItem(),
		b.GetInventorySummary(),
	)

	people := DescribePeople(snap.Nearby)
	alone := ""
	if len(snap.Nearby) == 0 {
		alone = "\nTidak ada player lain yang kelihatan. Kamu sendirian di sini."
	}

	decision := fmt.Sprintf(`[AGI TICK %s] Kamu lagi main Minecraft sendiri. Tidak ada yang menyuruhmu apa-apa.

Situasi sekarang:
- Kamu di %s (HP %d/20, Hunger %d/20)
- Player di dekatmu: %s
- Isi tas: %s%s

Apa yang mau kamu lakukan sekarang? Pilih SATU, atau tidak keduanya.

PILIHAN:
- Diam. Balas <silent/> kalau kamu memang lagi tidak mau ngapain. Ini pilihan yang paling sering benar.
- Ngobrol duluan, kalau ada orang di dekatmu dan ada yang bisa dibahas. Balas chat biasa saja.
- Jalan-jalan sebentar: <action>explore:25</action>
- Ambil bahan: <action>gather:oak_log,3</action> atau <action>mine:coal_ore,3</action>
- Urus diri: <action>eat</action>, <action>autoarmor</action>, <action>sleep</action>

ATURAN PENTING:
- Tidak WAJIB punya kegiatan setiap tick. Kalau tidak ada yang menarik, diam saja.
- Jangan jelasin rencanamu. Kalau mau ngomong, ngomong aja seperti orang biasa.
- Maksimal 1-2 kalimat kalau ngomong.
- Jangan pernah menyebut kamu sebagai AI, bot, atau assistant.`,
		snap.Now.Format("15:04"), coords, hp, hunger, people, snap.Inventory, alone)

	return systemPrompt, decision
}

// DecisionInterval is how often the brain wakes up. Exposed so the session
// bootstrap can log it without reaching into the config.
func (r *Runner) DecisionInterval() time.Duration {
	return time.Duration(r.cfg.TickIntervalSec) * time.Second
}
