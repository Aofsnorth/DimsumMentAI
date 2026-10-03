package gathering_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// The bot walked away from a half-standing tree.
//
// A player watching saw it fell several logs, then turn and head for the next
// trunk with the tree it had just been chopping still standing above it. Chopping
// N logs is not felling a tree, and the gap is invisible in the log: the gather
// hits its target, the loop moves on, and every line reports success.
//
// The cause was a cap where none belonged. collectLogBlocks walked the trunk with
// a BFS that stopped the moment it had found `targetCount` logs — so the target
// for "how much wood do I want" was deciding "how much of this tree do I fell".
// The two are unrelated. A trunk is either down or it is not.
//
// This is a source check, matching the existing break-path and looter guards. The
// behaviour needs a bot with a populated world model and a tree to look at, and
// the cheap rule is the one that actually broke: the walk must not be bounded by
// the target.

// chopActionSource parses internal/bot/gathering/chop_action.go.
func chopActionSource(t *testing.T) *ast.File {
	t.Helper()

	path := filepath.Join("..", "..", "..", "internal", "bot", "gathering", "chop_action.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return file
}

func mustFuncNamed(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
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

// mentionsIdent reports whether an expression names the given identifier.
func mentionsIdent(expr ast.Expr, name string) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return true
	})
	return found
}

func TestTheTrunkWalkIsNotCappedByTheTarget(t *testing.T) {
	t.Parallel()

	file := chopActionSource(t)
	fn := mustFuncNamed(t, file, "collectLogBlocks")

	// Anything at all mentioning the target inside this function is the defect:
	// how much wood was asked for has no business bounding which logs of a tree
	// the bot decides to fell.
	for _, param := range fn.Type.Params.List {
		for _, name := range param.Names {
			if name.Name != "targetCount" {
				continue
			}
			t.Errorf("collectLogBlocks still takes a %q parameter.\n"+
				"\tA target is how much wood the player wants; a trunk is how much tree "+
				"\tthere is. Passing the first to the second is what left trees half felled.", name.Name)
		}
	}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		expr, ok := n.(ast.Expr)
		if !ok {
			return true
		}
		if mentionsIdent(expr, "targetCount") {
			t.Errorf("collectLogBlocks still references targetCount in its body.\n" +
				"\tThe BFS must drain the whole queue; stopping once enough logs are " +
				"\tfound is what made the bot walk away from a standing tree.")
		}
		return true
	})
}

// TestTheTrunkWalkDrainsItsQueue pins the shape directly. The loop condition is
// the entire bug: `len(queue) > 0 && len(logBlocks) < targetCount` and
// `len(queue) > 0` are one token apart and mean opposite things to a player
// watching a tree.
func TestTheTrunkWalkDrainsItsQueue(t *testing.T) {
	t.Parallel()

	file := chopActionSource(t)
	fn := mustFuncNamed(t, file, "collectLogBlocks")

	var found bool
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		forStmt, ok := n.(*ast.ForStmt)
		if !ok {
			return true
		}
		if forStmt.Cond == nil {
			return true
		}
		found = true
		cond := forStmt.Cond
		if mentionsIdent(cond, "logBlocks") {
			t.Errorf("the trunk walk stops on logBlocks length (%v).\n"+
				"\tIt must stop only when the queue is empty, or the tree keeps its top.", cond)
		}
		return true
	})

	if !found {
		t.Error("no BFS loop found in collectLogBlocks; this test no longer describes the code it guards")
	}
}
