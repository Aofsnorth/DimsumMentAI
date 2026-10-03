package gathering_test

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A log walled in by a block was never cleared — it was given up on.
//
// The player watches this go: the bot walks up to a tree, a block is sitting in
// front of the log it needs, and instead of knocking it out of the way the bot
// stands there, gives up on that log, and eventually walks to the next tree with
// the first one still half standing.
//
// The cause was ordering, and ordering is invisible in the code's shape. Both
// clearing and planning lived in chopLogBlock, but clearing ran AFTER:
//
//	step, visible := PlanMineStep(world, botPos, pos)
//	if !visible {
//	    return false              // <- a walled-in log dies here
//	}
//	tc.clearObstructions(ctx, step)   // <- too late; never reached
//
// PlanMineStep needs one clear face out of six. A log boxed in on all six has
// none, so it returned false, and the one function written to clear the way was
// below the line that gave up. The swap is one statement, which is why it is worth
// pinning rather than trusting to review.
//
// Source check, matching the other drift guards in this repo: driving the real
// function needs a bot with a populated world model, and the rule that broke is
// expressible in three lines.

// chopSource parses internal/bot/gathering/chop_action.go.
func chopSource(t *testing.T) *ast.File {
	t.Helper()

	path := filepath.Join("..", "..", "..", "internal", "bot", "gathering", "chop_action.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return file
}

func chopFunc(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("function %s not found; this test no longer describes the code it guards", name)
	return nil
}

// lineOrder returns the byte offset of every call to name inside a function.
//
// It matches both x.f(...) and a bare f(...). PlanMineStep is a same-package
// function called unqualified, so matching only the selector form would report
// it as never called — which is how this file shipped a guard that could not
// fail.
func callOffsets(fn *ast.FuncDecl, name string) []int {
	var found []int
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			if fun.Name == name {
				found = append(found, int(call.Pos()))
			}
		case *ast.SelectorExpr:
			if fun.Sel.Name == name {
				found = append(found, int(call.Pos()))
			}
		}
		return true
	})
	return found
}

// TestObstructionsAreClearedBeforeTheLogIsJudged is the regression proper.
func TestObstructionsAreClearedBeforeTheLogIsJudged(t *testing.T) {
	t.Parallel()

	file := chopSource(t)
	fn := chopFunc(t, file, "chopLogBlock")

	clear := callOffsets(fn, "clearObstructions")
	plan := callOffsets(fn, "PlanMineStep")
	if len(clear) == 0 {
		t.Fatal("chopLogBlock never clears obstructions.\n" +
			"\tA log boxed in on all six faces has no mineable face, so PlanMineStep " +
			"\treturns false and the log is deferred and abandoned — the tree keeps " +
			"\tits lower half and the bot walks to the next one.")
	}
	if len(plan) == 0 {
		t.Fatal("chopLogBlock never plans a mine step; this test no longer describes the code it guards")
	}

	clearAt, planAt := clear[0], plan[0]

	if clearAt > planAt {
		t.Errorf("clearObstructions is called at byte %d, AFTER PlanMineStep at byte %d.\n"+
			"\tThe visibility check is what decides the log is unreachable, so clearing "+
			"has to happen before it or the block standing in the way is never cleared.",
			clearAt, planAt)
	}
}

// TestTheGuardStillRefusesALogItCannotReach is the guard on the guard. Moving
// the clear earlier must not turn the deferral into "always proceed": a log the
// bot still cannot see after clearing is a log it should leave, not one it should
// swing at blindly.
func TestTheGuardStillRefusesALogItCannotReach(t *testing.T) {
	t.Parallel()

	file := chopSource(t)
	fn := chopFunc(t, file, "chopLogBlock")

	src := renderFunc(t, fn)
	if !strings.Contains(src, "if !visible") {
		t.Error("chopLogBlock no longer refuses a log it cannot see.\n" +
			"\tClearing earlier makes the refusal reachable in more cases, not fewer.")
	}
	if !strings.Contains(src, "return false") {
		t.Error("chopLogBlock never bails out; a deferred log must still be deferred")
	}
}

// TestObstructionClearingReachesMoreThanTheTop pins the second half of the fix.
//
// It used to check exactly one cell — the block above the log — which handles
// leaves and moss but not a block in front of or below the log, which is the case
// the player reported. The sides are only worth breaking when the log has no open
// face, so the guard on this is that the side check is conditioned on that rather
// than applied unconditionally.
func TestObstructionClearingReachesMoreThanTheTop(t *testing.T) {
	t.Parallel()

	file := chopSource(t)
	fn := chopFunc(t, file, "clearObstructions")
	if fn == nil {
		t.Fatal("clearObstructions not found; this test no longer describes the code it guards")
	}

	src := renderFunc(t, fn)
	if !strings.Contains(src, "mineFaces") {
		t.Error("clearObstructions only ever inspects the block above the log.\n" +
			"\tA block in front of the log is the case that had no handler at all.")
	}
	if !strings.Contains(src, "PlanMineStep") {
		t.Error("clearObstructions never re-checks whether the log became reachable; " +
			"it would break blocks the bot did not need to break.")
	}
}

// TestTheImmovableRefusalIsOnTheBreakingPath is that check's other half.
//
// clearObstructions decides WHICH block to break; breakObstruction is the code
// that actually swings at it. The refusal therefore has to live in the second,
// and asserting it in the first would pass against a function that cannot break
// anything.
func TestTheImmovableRefusalIsOnTheBreakingPath(t *testing.T) {
	t.Parallel()

	file := chopSource(t)
	breaker := chopFunc(t, file, "breakObstruction")
	if breaker == nil {
		t.Fatal("breakObstruction not found; this test no longer describes the code it guards")
	}

	src := renderFunc(t, breaker)
	if !strings.Contains(src, "immovableBlockName") {
		t.Error("breakObstruction does not consult immovableBlockName.\n" +
			"\tIt now decides on its own which block to destroy, so bedrock and barrier " +
			"\tneed an explicit refusal or a player's wall becomes fair game.")
	}
	if !strings.Contains(src, "isLogBlockName") {
		t.Error("breakObstruction does not skip other logs.\n" +
			"\tA trunk's own logs are not obstructions; clearing through one would " +
			"\tdestroy the tree the bot was sent to fell.")
	}
}

// renderFunc prints a function's source back out of the parsed file.
func renderFunc(t *testing.T, fn *ast.FuncDecl) string {
	t.Helper()

	path := filepath.Join("..", "..", "..", "internal", "bot", "gathering", "chop_action.go")
	src, err := readFileRange(path, fn.Pos(), fn.End())
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return src
}

// readFileRange slices raw bytes out of a file by the byte offsets the parser
// reported, which is the only way to get a declaration's own source back without
// reformatting it.
func readFileRange(path string, from, to token.Pos) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if int(from) > len(raw) || int(to) > len(raw) || from > to {
		return "", errRange
	}
	return string(raw[from:to]), nil
}

// errRange is what readFileRange returns when the parser handed back offsets
// that do not describe this file.
var errRange = errors.New("byte offsets are outside the file")
