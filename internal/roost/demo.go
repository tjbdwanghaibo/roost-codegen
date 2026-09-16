package roost

import (
	"fmt"
	"go/format"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tjbdwanghaibo/roost-codegen/demo"
)

// demoModulePlaceholder is replaced with the project's module path.
const demoModulePlaceholder = "{{MODULE}}"

// demoTemplateName is the opt-in value of `roost project new … -template`.
const demoTemplateName = "game-demo"

// writeDemoFile renders one embedded template into the project. The result is
// application-owned: it carries no generated header and nothing rewrites it
// afterwards, so `project sync` and `project upgrade` leave the developer's
// edits alone. Several of these deliberately overwrite a scaffold that `Add`
// has just produced — the scaffold supplies the wiring (the Entity field, the
// DAO binding, the manifest entry), and the demo supplies the body.
func writeDemoFile(root, module, rel string) error {
	raw, err := demo.Files.ReadFile(rel + ".tmpl")
	if err != nil {
		return fmt.Errorf("template %s: read demo source: %w", demoTemplateName, err)
	}
	body := strings.ReplaceAll(string(raw), demoModulePlaceholder, module)
	if strings.HasSuffix(rel, ".go") {
		formatted, formatErr := format.Source([]byte(body))
		if formatErr != nil {
			return fmt.Errorf("template %s: format %s: %w", demoTemplateName, rel, formatErr)
		}
		body = string(formatted)
	}
	return writeAtomic(filepath.Join(root, filepath.FromSlash(rel)), []byte(body), 0o644)
}

// demoSourceFiles lists every template this package ships, so a test can hold
// the embedded set and the scaffold steps to the same list.
func demoSourceFiles() ([]string, error) {
	var out []string
	err := fs.WalkDir(demo.Files, ".", func(p string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		out = append(out, strings.TrimSuffix(p, ".tmpl"))
		return nil
	})
	return out, err
}

// applyDemoTemplate is the game template plus what the demo's own code needs
// from the manifest: protocol (its request message), dao (its persistent
// Player state), config (the item table) and errcode (its coded failures).
// All are on by default; a caller who narrowed -features would otherwise get
// a project that cannot build.
func applyDemoTemplate(m *Manifest, gameService string) error {
	if err := applyGameTemplate(m, gameService); err != nil {
		return err
	}
	for _, feature := range []string{"protocol", "entity", "nest", "dao", "config", "errcode"} {
		if !contains(m.Features, feature) {
			m.Features = append(m.Features, feature)
		}
	}
	sort.Strings(m.Features)
	return nil
}

// demoScaffoldStep is one step of the demo build-out: either a generator
// command or a template write. They are ordered, and the order carries
// meaning — `add endpoint` parses the handler's parameters and the protocol
// message's fields and refuses to wire a pair that does not match, so both
// bodies must be on disk before it runs.
type demoScaffoldStep struct {
	add   *AddOptions
	write string
	why   string
}

func demoScaffoldSteps(gameService string) []demoScaffoldStep {
	return []demoScaffoldStep{
		{add: &AddOptions{Kind: "component", Name: "Profile", Entity: "Player"}, why: "Player's identity state"},
		{add: &AddOptions{Kind: "component", Name: "Bag", Entity: "Player"}, why: "Player's item state"},
		{add: &AddOptions{Kind: "dao", Name: "Player", Entity: "Player"}, why: "persistence for both components"},
		{write: "db/def/player.go", why: "the persistent fields the generated mutators are built from"},
		{write: "game/entities/player/profile_component.go", why: "rename and level-up through generated mutators"},
		{write: "game/entities/player/bag_component.go", why: "add-item: table lookup, coded errors, generated map mutators"},
		{add: &AddOptions{Kind: "table", Name: "Item"}, why: "the item config table"},
		{write: "configs/schema/item.go", why: "the table's columns and rules"},
		{write: "configs/table/item.csv", why: "the rows; converted to configs/data/item.json by generate"},
		{add: &AddOptions{Kind: "errcode", Name: "ItemUnknown", ID: 100001}, why: "a coded failure in the manifest's errcode space"},
		{add: &AddOptions{Kind: "errcode", Name: "ItemCount", ID: 100002}, why: "a coded failure in the manifest's errcode space"},
		{add: &AddOptions{Kind: "errcode", Name: "BagFull", ID: 100003}, why: "a coded failure in the manifest's errcode space"},
		{write: "internal/errors/item_unknown.go", why: "client-facing message instead of the TODO placeholder"},
		{write: "internal/errors/item_count.go", why: "client-facing message instead of the TODO placeholder"},
		{write: "internal/errors/bag_full.go", why: "client-facing message instead of the TODO placeholder"},
		{add: &AddOptions{Kind: "handler", Name: "AddItem", Entity: "Player", Component: "Bag"}, why: "the write transaction"},
		{write: "game/handler/add_item.go", why: "handler parameters and result; the Sender and endpoint are generated from them"},
		{add: &AddOptions{Kind: "access", Name: "player", Service: gameService}, why: "the player request boundary"},
		{add: &AddOptions{Kind: "transport", Name: "tcp"}, why: "a transport a client can actually connect to"},
		{write: "internal/access/player/tcp/auth.go", why: "a demo credential so the flow can be driven end to end"},
		{add: &AddOptions{Kind: "protocol", Name: "AddItem", Group: "game", Handler: "player"}, why: "the wire message"},
		{write: "protocol/def/add_item.go", why: "request fields matching the handler parameters; response with code and count"},
		{add: &AddOptions{Kind: "endpoint", Name: "AddItem", Handler: "player"}, why: "protocol boundary to Nest sender"},
		{write: "game/controllers/player/add_item.go", why: "the error boundary: coded errors become the response, not a dropped connection"},
		{write: "internal/service/account/collaborators.go", why: "an account service that can log a demo user in and mint ids from Redis"},
	}
}

// scaffoldDemoTemplate builds the demo on top of the game template: Player
// gains Profile and Bag components backed by a DAO, an item table and three
// coded errors, one Nest write transaction adds an item, a TCP endpoint
// carries it and turns failures into coded responses, and the account service
// gets collaborators that work. Everything it writes is application-owned and
// written once.
func scaffoldDemoTemplate(root string, m Manifest, gameService string) ([]string, error) {
	created, err := scaffoldGameTemplate(root, m, gameService)
	if err != nil {
		return created, err
	}
	for _, step := range demoScaffoldSteps(gameService) {
		if step.add != nil {
			files, addErr := Add(root, *step.add)
			if addErr != nil {
				return created, fmt.Errorf("template %s: add %s %s (%s): %w", demoTemplateName, step.add.Kind, step.add.Name, step.why, addErr)
			}
			created = append(created, files...)
			continue
		}
		if writeErr := writeDemoFile(root, m.Project.Module, step.write); writeErr != nil {
			return created, fmt.Errorf("template %s: write %s (%s): %w", demoTemplateName, step.write, step.why, writeErr)
		}
		created = append(created, step.write)
	}
	if err := Generate(root, GenerateOptions{Stdout: io.Discard}); err != nil {
		return created, fmt.Errorf("template %s: regenerate: %w", demoTemplateName, err)
	}
	return created, nil
}
