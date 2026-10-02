package animation_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bedrock-ai/internal/bot/movement/animation"
)

// A break path that does not use the shared rhythm looks like a player and is
// not one.
//
// Three build paths did exactly this: send a single MineSwing, sleep a hardcoded
// 300-500ms, predict the destroy. It was wrong twice over. Visibly, one swing
// and a frozen head is the least human thing a breaking body can do. Mechanically,
// a fixed sleep is right for exactly one block — for obsidian it predicts the
// destroy seconds before the server agrees the block is gone, and a
// server-authoritative host rejects an early PredictDestroy without a word, so
// the block simply survives.
//
// This file checks both halves without needing a server.

// TestNoBreakPathHandRollsItsOwnSwing is the drift guard.
//
// It is a source check on purpose. The alternative — a test that calls digBlock
// and counts packets — needs a live connection, a world model and a running
// server, which is exactly the coverage nobody had when this shipped. A rule
// that is cheap to state and cheap to check is the only one that survives.
func TestNoBreakPathHandRollsItsOwnSwing(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", "..", "..", "internal", "bot"))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	var offenders []string
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if perr != nil {
			return perr
		}
		rel, _ := filepath.Rel(root, path)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// A function that breaks a block is one that starts a break.
			if !mentionsBreakAction(fn) {
				continue
			}
			// A hand-rolled break waits for itself and does not use the rhythm.
			//
			// A function with no wait at all is a router — breaking.go translates
			// a legacy PlayerAction into the server-authoritative BlockActions
			// form and deliberately has no clock in it. A function that only
			// starts a break, or only predicts the destroy, is the legitimate
			// two-call split. Both are left alone.
			if walksBeats(fn) || !waitsForItself(fn) {
				continue
			}
			offenders = append(offenders, rel+":"+fn.Name.Name+
				" waits out a break with a hand-rolled interval instead of the shared rhythm")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the bot package: %v", err)
	}

	for _, offender := range offenders {
		t.Errorf("%s", offender+
			"\n\tUse animation.Beats(gathering.BreakDuration(bot, block), centre) "+
			"and send a swing on every beat after the wind-up.")
	}
}

// TestABreakNeverOutlastsItsBlock is the other half, and it is the half that was
// silently wrong: the sleep did not know how long the block took.
func TestABreakNeverOutlastsItsBlock(t *testing.T) {
	t.Parallel()

	// Dirt with a shovel is the fast case, obsidian bare-handed the slow one.
	// A single hardcoded sleep cannot be right for both, and the slow one is the
	// one that fails silently.
	fast := totalBeatTime(150 * time.Millisecond)
	slow := totalBeatTime(9 * time.Second)

	if fast == slow {
		t.Error("every break takes the same time regardless of the block: " +
			"the rhythm is not reading the block's real break duration")
	}
	if slow <= 5*time.Second {
		t.Errorf("a nine-second break is paced as %v: the arm stops swinging long "+
			"before the block breaks, which is what a player watching sees", slow)
	}
	// A fast break still has to be allowed one swing, or an instant block reads
	// as the block simply vanishing.
	if fast <= animation.WindUpMin {
		t.Errorf("a %v break is paced as %v, which is the wind-up alone: no swing "+
			"at all, so the block vanishes between beats", 150*time.Millisecond, fast)
	}
}

func totalBeatTime(breakTime time.Duration) time.Duration {
	var total time.Duration
	for _, b := range animation.Beats(breakTime, [3]float32{}) {
		total += b.Wait
	}
	return total
}

// mentionsBreakAction reports whether a function body both starts a break and
// predicts its destroy.
//
// Both, not either. A break legitimately spans two functions — the miner sends
// StartBreak, waits out the rhythm, and finishes in a different call — so a rule
// that demanded every starter also pace swings would be demanding that each
// function contain the whole break. The defect is narrower and worse than that:
// a function that predicts the destroy without ever pacing a swing in between
// has decided the block is gone without doing the work.
func mentionsBreakAction(fn *ast.FuncDecl) bool {
	var starts, predicts bool
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != "protocol" {
			return true
		}
		switch sel.Sel.Name {
		case "PlayerActionStartBreak":
			starts = true
		case "PlayerActionPredictDestroyBlock":
			predicts = true
		}
		return true
	})
	return starts && predicts
}

// walksBeats reports whether the function paces itself with the shared rhythm.
func walksBeats(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "animation" && sel.Sel.Name == "Beats" {
			found = true
		}
		return true
	})
	return found
}

// waitsForItself reports whether the function spends wall-clock time inside the
// break rather than finishing it instantly.
func waitsForItself(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "time" {
			return true
		}
		switch sel.Sel.Name {
		case "Sleep", "After", "Tick", "NewTimer":
			found = true
		}
		return true
	})
	return found
}
