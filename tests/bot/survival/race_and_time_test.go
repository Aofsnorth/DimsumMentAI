package survival_test

import (
	"sync"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/pathfinder"
	"bedrock-ai/internal/bot/survival"
	"bedrock-ai/internal/config"
)

func TestSurvivalSetWorldTimeNegativeModuloAndConcurrentToggles(t *testing.T) {
	b := bot.NewBot(nil, &config.ServerConfig{}, &config.AIConfig{}, pathfinder.NewLocalWorldModel())
	m := survival.NewManager(b)
	m.SetWorldTime(-100)
	if got := m.GetWorldTime(); got != 23900 {
		t.Fatalf("expected normalized world time 23900 for -100 ticks, got %d", got)
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func(v int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				enable := (v+j)%2 == 0
				m.EnableAutoEat(enable)
				m.EnableAutoArmor(enable)
				m.EnableAutoTorch(enable)
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = m.ShouldAutoEat()
				_ = m.ShouldUpgradeArmor()
			}
		}()
	}
	wg.Wait()
}
