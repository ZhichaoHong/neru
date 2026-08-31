package architecture_test

import (
	"go/ast"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// overlayWindowConstructorSites maps each platform overlay-window constructor to
// the functions in internal/adapter/overlay/windows allowed to call it.
//
// The screen-share affinity (WDA_EXCLUDEFROMCAPTURE) is a property of the HWND,
// not of the Go value wrapping it, so a window that is created without it is
// capturable until the next SetSharingType. Every creation path therefore funnels
// through a function that applies the manager's current choice:
//
//   - newOverlayWindowAt is the Manager's choke point for the badge windows; it
//     reads hideInScreenShare under renderMu.
//   - newWinOverlay and recreateWindow build the shared grid surface, which
//     remembers the affinity on winOverlay because recreateWindow replaces the
//     platform window rather than reviving it. ensureWinOverlayLocked reapplies
//     after newWinOverlay.
//
// A new call site elsewhere is a window that appears in a screen share, which is
// invisible in review and only shows up in someone else's recording - hence a
// pin rather than a comment.
var overlayWindowConstructorSites = map[string][]string{
	"NewOverlayWindowAt": {"newOverlayWindowAt"},
	"NewOverlayWindow":   {"newWinOverlay", "recreateWindow"},
}

const winPlatformPackage = "github.com/y3owk1n/neru/internal/adapter/platform/windows"

func TestOverlayWindowsCreatesWindowsThroughAffinityChokePoints(t *testing.T) {
	repoRoot := findRepoRoot(t)
	packageDir := filepath.Join(repoRoot, "internal", "adapter", "overlay", "windows")

	seen := make(map[string]int, len(overlayWindowConstructorSites))

	for _, file := range parsedGoFiles(t, packageDir) {
		qualifier := platformPackageQualifier(file)
		if qualifier == "" {
			continue
		}

		for _, decl := range file.Decls {
			funcDecl, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || funcDecl.Body == nil {
				continue
			}

			for _, constructor := range constructorsCalled(funcDecl, qualifier) {
				allowed, pinned := overlayWindowConstructorSites[constructor]
				if !pinned {
					continue
				}

				seen[constructor]++

				if !slices.Contains(allowed, funcDecl.Name.Name) {
					t.Errorf(
						"%s calls %s.%s; overlay windows are created only from %s so the "+
							"screen-share affinity is applied at creation "+
							"(internal/adapter/overlay/windows/manager.go, SetSharingType)",
						funcDecl.Name.Name,
						qualifier,
						constructor,
						strings.Join(allowed, " or "),
					)
				}
			}
		}
	}

	for constructor := range overlayWindowConstructorSites {
		if seen[constructor] == 0 {
			t.Errorf(
				"no call to %s found in internal/adapter/overlay/windows; the constructor "+
					"was renamed or removed and this pin is dead",
				constructor,
			)
		}
	}
}

// platformPackageQualifier names the identifier a file refers to the Windows
// platform package by, or "" when the file does not import it.
func platformPackageQualifier(file *ast.File) string {
	for name, path := range importPathsIn(file) {
		if path == winPlatformPackage {
			return name
		}
	}

	return ""
}

// constructorsCalled lists the selector names a function calls on one package
// qualifier - the "NewOverlayWindow" of winplatform.NewOverlayWindow().
func constructorsCalled(funcDecl *ast.FuncDecl, qualifier string) []string {
	var called []string

	ast.Inspect(funcDecl.Body, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}

		selector, isSelector := call.Fun.(*ast.SelectorExpr)
		if !isSelector {
			return true
		}

		if ident, isIdent := selector.X.(*ast.Ident); isIdent && ident.Name == qualifier {
			called = append(called, selector.Sel.Name)
		}

		return true
	})

	return called
}
