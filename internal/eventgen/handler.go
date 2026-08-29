package eventgen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// HandlerInfo holds all DealEvent methods for a single receiver in a file.
type HandlerInfo struct {
	Package  string
	Receiver string // receiver type name, e.g. "Player"
	EventPkg string
	Events   []HandlerEvent
	FilePath string // source file path
}

// HandlerEvent is a single DealEventXXX method found.
type HandlerEvent struct {
	Suffix   string // e.g. "PlayerOnLine"
	EventPkg string // import path for event package
}

// scanGameDir walks gameDir for DealEventXXX methods and generates handler code.
func scanGameDir(gameDir string, eventPkg string, force bool) error {
	return scanGameDirTo(gameDir, eventPkg, force, io.Discard)
}

func scanGameDirTo(gameDir string, eventPkg string, force bool, stdout io.Writer) error {
	var allHandlers []HandlerInfo

	err := filepath.Walk(gameDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || base == "vendor" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_gen.go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		handlers, err := scanFile(path, eventPkg)
		if err != nil {
			return err
		}
		allHandlers = append(allHandlers, handlers...)
		return nil
	})
	if err != nil {
		return err
	}

	// Group by directory + receiver
	type fileKey struct {
		dir      string
		receiver string
	}
	grouped := make(map[fileKey]*HandlerInfo)
	for i := range allHandlers {
		h := &allHandlers[i]
		key := fileKey{dir: filepath.Dir(h.FilePath), receiver: h.Receiver}
		if existing, ok := grouped[key]; ok {
			existing.Events = append(existing.Events, h.Events...)
		} else {
			grouped[key] = h
		}
	}

	// Generate per receiver
	for key, h := range grouped {
		// Sort events for deterministic output
		sort.Slice(h.Events, func(i, j int) bool {
			return h.Events[i].Suffix < h.Events[j].Suffix
		})

		outFile := filepath.Join(key.dir, fmt.Sprintf("%s_event_gen.go", toSnakeHandler(key.receiver)))
		changed, err := generateHandler(h, outFile, force)
		if err != nil {
			return fmt.Errorf("generate handler %s: %w", key.receiver, err)
		}
		if changed {
			fmt.Fprintf(stdout, "generated: %s\n", outFile)
		}
	}

	return nil
}

// scanFile parses a single file for DealEventXXX methods.
func scanFile(filePath string, eventPkg string) ([]HandlerInfo, error) {
	fset := token.NewFileSet()
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	f, err := parser.ParseFile(fset, filePath, content, parser.ParseComments)
	if err != nil {
		return nil, nil // skip unparseable files
	}

	pkg := f.Name.Name

	// receiver → events
	receiversMap := make(map[string][]HandlerEvent)

	for _, decl := range f.Decls {
		funcDecl, ok := decl.(*ast.FuncDecl)
		if !ok || funcDecl.Recv == nil {
			continue
		}

		name := funcDecl.Name.Name
		if !strings.HasPrefix(name, "DealEvent") {
			continue
		}

		suffix := strings.TrimPrefix(name, "DealEvent")
		if len(suffix) == 0 || !unicode.IsUpper(rune(suffix[0])) {
			continue
		}

		// Get receiver type
		recv := receiverType(funcDecl.Recv)
		if recv == "" {
			continue
		}

		receiversMap[recv] = append(receiversMap[recv], HandlerEvent{
			Suffix:   suffix,
			EventPkg: eventPkg,
		})
	}

	var handlers []HandlerInfo
	for recv, events := range receiversMap {
		handlers = append(handlers, HandlerInfo{
			Package:  pkg,
			Receiver: recv,
			EventPkg: eventPkg,
			Events:   events,
			FilePath: filePath,
		})
	}

	return handlers, nil
}

// receiverType extracts the type name from a method receiver.
func receiverType(fieldList *ast.FieldList) string {
	if fieldList == nil || len(fieldList.List) == 0 {
		return ""
	}
	field := fieldList.List[0]
	switch t := field.Type.(type) {
	case *ast.StarExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return ident.Name
		}
	case *ast.Ident:
		return t.Name
	}
	return ""
}

func toSnakeHandler(s string) string {
	var result strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 && !unicode.IsUpper(rune(s[i-1])) {
				result.WriteByte('_')
			}
			result.WriteRune(unicode.ToLower(r))
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}
