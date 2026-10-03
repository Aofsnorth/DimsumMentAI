package gathering_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// The sweep logged the drop and then never collected it.
//
// A live run: "Looter: heading to item drop id=87 pos=[-40.537 67.125 -20.233]",
// then "recalculating path using A* target_y=67", then "A* gave up short:
// route does not reach the destination blocks_below_target=2", then "Wood broken
// but not picked up". The bot announced the drop, could not reach it, and
// reported a gather failure on work it had done.
//
// The cause is which navigation the sweep calls. NavigateTo takes a raw world
// position, and an item's Y is the interior of the block it is lying in — not the
// feet level of a tile anything can stand on. A drop that settles a block below
// the body (under a felled trunk, or at the bottom of the one-block pit the bot
// digs mining down) has no standable tile at that height at all. A* routes as
// close as it can, the poll re-issues the same impossible destination every
// 250ms, and the body works the rim of the pit going nowhere: the tremor, the
// missed pickup, and a log full of "gave up short".
//
// NavigateToBlock resolves the target to the nearest standable tile, which is
// what makes it the only correct call here.
//
// This is a source check on purpose, matching the existing break-path guard in
// tests/bot/animation/break_rhythm_test.go. Asserting it end to end needs a fake
// implementing all 29 methods of gathering.Bot, and the cheap rule is the one
// that actually matters: which function does this loop call.

// looterSource parses internal/bot/gathering/looter.go.
func looterSource(t *testing.T) *ast.File {
	t.Helper()

	path := filepath.Join("..", "..", "..", "internal", "bot", "gathering", "looter.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return file
}

// marksAttempted reports whether an assignment target writes to the attempted
// set. Both `attempted[id]` and a bare `attempted` count.
func marksAttempted(expr ast.Expr) bool {
	switch target := expr.(type) {
	case *ast.Ident:
		return target.Name == "attempted"
	case *ast.IndexExpr:
		return marksAttempted(target.X)
	}
	return false
}

// funcNamed returns the declaration of a function, or nil.
func funcNamed(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

// callsNamedMethod reports how many times a function body calls recv.MethodName.
func callsNamedMethod(fn *ast.FuncDecl, method string) int {
	found := 0
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == method {
			found++
		}
		return true
	})
	return found
}

func TestTheSweepWalksToAStandableTileNotTheItemsRawPosition(t *testing.T) {
	t.Parallel()

	file := looterSource(t)
	fn := funcNamed(file, "collectDrop")
	if fn == nil {
		t.Fatal("collectDrop not found in looter.go; this test no longer describes the code it guards")
	}

	if callsNamedMethod(fn, "NavigateToBlock") == 0 {
		t.Error("collectDrop does not call NavigateToBlock.\n" +
			"\tA drop's Y is the inside of a block, not a feet level. NavigateToBlock snaps " +
			"the target to the nearest standable tile;\n\tNavigateTo does not, and the bot ends " +
			"up working the rim of the pit a drop fell into (A* gave up short, no pickup, tremor).")
	}
}

func TestTheSweepDoesNotWalkToARawItemPosition(t *testing.T) {
	t.Parallel()

	file := looterSource(t)
	fn := funcNamed(file, "collectDrop")
	if fn == nil {
		t.Fatal("collectDrop not found in looter.go; this test no longer describes the code it guards")
	}

	if calls := callsNamedMethod(fn, "NavigateTo"); calls > 0 {
		t.Errorf("collectDrop calls NavigateTo %d time(s) with a raw position.\n"+
			"\tDrop the raw walk and use NavigateToBlock, which resolves the tile to one the "+
			"body can stand on.", calls)
	}
}

// TestAnUnreachableDropIsSkippedRatherThanReissued guards the other half of the
// fix: when there is nowhere left to walk, the sweep has to give the item up. It
// used to keep re-issuing navigation toward a tile it could never reach, which is
// what the body visibly shook about. Abandoning is cheap; looping is not.
func TestAnUnreachableDropIsSkippedRatherThanReissued(t *testing.T) {
	t.Parallel()

	file := looterSource(t)
	fn := funcNamed(file, "collectDrop")
	if fn == nil {
		t.Fatal("collectDrop not found in looter.go; this test no longer describes the code it guards")
	}

	var abandons, retries bool
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		// A drop is given up on by writing to the attempted set. The assignment
		// target is a map index, so the identifier is under an IndexExpr rather
		// than being the expression itself.
		if assign, ok := n.(*ast.AssignStmt); ok {
			for _, lhs := range assign.Lhs {
				if marksAttempted(lhs) {
					abandons = true
				}
			}
		}
		// A bare `continue` on the unreachable branch is the spin: the loop
		// re-reads the same drop and issues the same impossible navigation.
		if branch, ok := n.(*ast.BranchStmt); ok && branch.Tok == token.CONTINUE {
			retries = true
		}
		return true
	})

	if !abandons {
		t.Error("collectDrop never marks a drop as attempted; an unreachable item would be " +
			"retried forever instead of being skipped")
	}
	if !retries {
		t.Error("collectDrop has no path that keeps polling a drop it cannot walk to.\n" +
			"\tThere are two cases and they are not the same: already within pickup reach, " +
			"where waiting is right, and out of reach of any standable tile, where the item " +
			"must be abandoned. Without the continue, the bot cannot tell them apart.")
	}
}
