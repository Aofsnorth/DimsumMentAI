package agi

import (
	"fmt"
	"strings"
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

	// The bot-derived preamble is optional. Only the system prompt needs a live
	// client, and the decision text below does not — so a runner with no bot can
	// still produce it. That is not a test convenience: the interesting half of
	// this function is the half that says what the bot knows, and it is exactly
	// the half that was wrong for months without anybody noticing, because there
	// was no way to read it without a connection.
	var preamble string
	if b != nil {
		b.Mu.Lock()
		botName := b.Name
		b.Mu.Unlock()

		hp, hunger, coords := b.GetStatusDetails()
		botStatusText := fmt.Sprintf("HP: %d/20, Hunger: %d/20", hp, hunger)

		preamble = b.AiClient.BuildSystemPrompt(
			botName,
			coords+" ("+botStatusText+")",
			"",
			b.GetHeldItem(),
			b.GetInventorySummary(),
		)
	}

	people := DescribePeople(snap.Nearby)
	alone := ""
	if len(snap.Nearby) == 0 {
		alone = "\nTidak ada player lain yang kelihatan. Kamu sendirian di sini."
	}
	coords := snap.Coords
	hp, hunger := snap.HP, snap.Hunger
	inventory := snap.Inventory
	if b != nil {
		hp, hunger, coords = b.GetStatusDetails()
		inventory = b.GetInventorySummary()
	}
	// Signage goes into the prompt for the same reason it goes into the Jev
	// state: a sign the bot can read is a plan, and a bot that knows a room is
	// labelled does not have to search it blind.
	signSummary := "tidak ada"
	if len(snap.VisibleSigns) > 0 {
		signSummary = strings.Join(snap.VisibleSigns, ", ")
	}

	decision := fmt.Sprintf(`[AGI TICK %s] Kamu lagi main Minecraft sendiri. Tidak ada yang menyuruhmu apa-apa.

Situasi sekarang:
- Kamu di %s (HP %d/20, Hunger %d/20)
- Player di dekatmu: %s
- Isi tas: %s%s
- Sign di sekitar: %s
%s
%s
%s
%s
Apa yang mau kamu lakukan sekarang? Pilih SATU, atau tidak keduanya.

PILIHAN:
- Diam. Balas <silent/> kalau kamu memang lagi tidak mau ngapain. Ini pilihan yang paling sering benar.
- Ngobrol duluan, kalau ada orang di dekatmu dan ada yang bisa dibahas. Balas chat biasa saja.
- Deketin orang itu: <action>come:Namanya</action>
- Jalan-jalan sebentar: <action>explore:25</action>
- Ambil bahan: <action>gather:oak_log,3</action>
- Nambang beneran: <action>automine:wood</action> (bikin kayu yang beneran keluar)
- Mau mancing: <action>fish</action> (bot cuma nelen kalau ada air di pandangan)
- Panen: <action>harvest</action> (cuma kalau tanaman sudah matang)
- Rawat hewan: <action>feed</action>
- Bikin sesuatu: <action>craft:nama_barang</action> (misal <action>craft:wooden_pickaxe,1</action>)
- Baca papan: <action>readsign</action> — buat cari tau isi ruangan
- Cari barang di chest: <action>take:nama_barang,3</action> (bot buka chest satu-satu, dan baca sign dulu kalau ada)
- Simpan barang: <action>store:nama_barang</action>
- Urus diri: <action>eat</action>, <action>autoarmor</action>, <action>sleep</action>
- gesture iseng: <action>emote:nod</action>

ATURAN PENTING:
- Tidak WAJIB punya kegiatan setiap tick. Kalau tidak ada yang menarik, diam saja.
- Kalau ada sign di dekatmu, baca DULUAN sebelum nyari barang — label di situ Petrovunjuk chest yang benar.
- Kalau ada rencana di atas, IKUTI. Jangan usik yang lagi dikerjakan; kamu yang ngejelasin, bukan yang ngarah.
- Kalau daftar "PILIHAN" tidak punya yang cocok dengan isi dunia ini, jangan karang aksi. Bicara aja.
- Jangan jelasin rencanamu. Kalau mau ngomong, ngomong aja seperti orang biasa.
- Maksimal 1-2 kalimat kalau ngomong.
- Jangan pernah menyebut kamu sebagai AI, bot, atau assistant.`,
		snap.Now.Format("15:04"), coords, hp, hunger, people, inventory, alone, signSummary,
		episodeBlock(snap), planBlock(snap), goalBlock(snap), worldBlock(snap))

	return preamble, decision
}

func (r *Runner) DecisionInterval() time.Duration {
	return time.Duration(r.cfg.TickIntervalSec) * time.Second
}

// planBlock, goalBlock, worldBlock and episodeBlock are the four things the
// decision prompt was missing for months: what the bot is in the middle of,
// why, what this world contains, and how much recording time is left.
//
// They were absent because the prompt was written when the bot had none of
// them. Adding a plan and a clock and then not telling the model about either
// is how you get a bot that announces one thing while its body does another —
// and a viewer watching that concludes the bot is broken rather than that two
// halves of it disagree.
func planBlock(snap Snapshot) string {
	if snap.PlanSummary == "" || snap.PlanSummary == "no plan" {
		return ""
	}
	return "\nYang lagi bot kerjain sekarang:\n" + snap.PlanSummary
}

func goalBlock(snap Snapshot) string {
	if snap.GoalSummary == "" {
		return ""
	}
	return "\nTujuan keseluruhan: " + snap.GoalSummary
}

func worldBlock(snap Snapshot) string {
	if snap.Vocabulary == nil {
		return ""
	}
	desc := snap.Vocabulary.Describe()
	if desc == "" || desc == "nothing observed yet" {
		return ""
	}
	return "\nIsi dunia ini:\n" + desc
}

// episodeBlock carries the recording brief and its clock, and what is in view.
//
// The clock is the part that changes behaviour. A model told to "do something
// interesting" during a 24 minute recording will happily propose a build that
// takes two hours, and the overrun is the part a viewer sees.
func episodeBlock(snap Snapshot) string {
	var sb strings.Builder
	if snap.EpisodeText != "" {
		sb.WriteString("\nRekaman ini untuk: " + snap.EpisodeText)
	}
	if snap.VisibleMob != "" && snap.VisibleMob != "none" {
		sb.WriteString("\nMakhluk yang kelihatan: " + snap.VisibleMob)
	}
	return sb.String()
}
