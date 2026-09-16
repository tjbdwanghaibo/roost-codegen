package roost

import (
	"fmt"
	"go/format"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tjbdwanghaibo/roost-codegen/demo"
)

// demoModulePlaceholder is replaced with the project's module path.
const demoModulePlaceholder = "{{MODULE}}"

// demoGameServicePlaceholder is replaced with the game service's name as it
// appears in paths and app.ServiceName; demoGameServicePackagePlaceholder
// with its Go package identifier. The demo's files under
// internal/service/game/ are written to internal/service/<name>/ so a
// project created with a different first service still gets them.
const (
	demoGameServicePlaceholder        = "{{GAME_SERVICE}}"
	demoGameServicePackagePlaceholder = "{{GAME_SERVICE_PKG}}"
	demoProjectPlaceholder            = "{{PROJECT}}"
	demoGameServiceDir                = "internal/service/game/"
)

// demoVars is what a template may refer to besides its own text.
type demoVars struct {
	module      string
	gameService string
	project     string
}

func (vars demoVars) apply(body string) string {
	body = strings.ReplaceAll(body, demoModulePlaceholder, vars.module)
	body = strings.ReplaceAll(body, demoProjectPlaceholder, vars.project)
	body = strings.ReplaceAll(body, demoGameServicePackagePlaceholder, safeIdent(vars.gameService))
	return strings.ReplaceAll(body, demoGameServicePlaceholder, vars.gameService)
}

// demoDestination maps a shipped template path to where it lands in the
// project: the same path, except under the game service's own directory.
func demoDestination(rel, gameService string) string {
	if rest, ok := strings.CutPrefix(rel, demoGameServiceDir); ok {
		return "internal/service/" + gameService + "/" + rest
	}
	return rel
}

// demoTemplateName is the opt-in value of `roost project new … -template`.
const demoTemplateName = "game-demo"

// writeDemoFile renders one embedded template into the project. The result is
// application-owned: it carries no generated header and nothing rewrites it
// afterwards, so `project sync` and `project upgrade` leave the developer's
// edits alone. Several of these deliberately overwrite a scaffold that `Add`
// has just produced — the scaffold supplies the wiring (the Entity field, the
// DAO binding, the manifest entry), and the demo supplies the body.
func writeDemoFile(root string, vars demoVars, rel string) (string, error) {
	raw, err := demo.Files.ReadFile(rel + ".tmpl")
	if err != nil {
		return "", fmt.Errorf("template %s: read demo source: %w", demoTemplateName, err)
	}
	body := vars.apply(string(raw))
	destination := demoDestination(rel, vars.gameService)
	if strings.HasSuffix(rel, ".go") {
		formatted, formatErr := format.Source([]byte(body))
		if formatErr != nil {
			return "", fmt.Errorf("template %s: format %s: %w", demoTemplateName, rel, formatErr)
		}
		body = string(formatted)
	}
	if err := writeAtomic(filepath.Join(root, filepath.FromSlash(destination)), []byte(body), 0o644); err != nil {
		return "", err
	}
	return destination, nil
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
	run   func(root, gameService string) error
	why   string
}

// enableDemoMatchSweep lists the demo's duel queue under match.sweep_queues in
// the match service's config, the way a deployment does. The match process
// then resolves expired duel tickets in the background; without it a lapsed
// ticket is only resolved when touched, and the process says so at start.
func enableDemoMatchSweep(root, _ string) error {
	path := filepath.Join(root, "configs", "service", "config.match.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	const before, after = "  sweep_queues: []\n", "  sweep_queues:\n    - duel:2:default\n"
	if !strings.Contains(string(raw), before) {
		return fmt.Errorf("%s: expected %q to replace", path, strings.TrimSpace(before))
	}
	return writeAtomic(path, []byte(strings.Replace(string(raw), before, after, 1)), 0o644)
}

// enableDemoPlayerTCP flips player_access.tcp.enabled in the game service's
// config, the same edit `roost config enable player-tcp` makes. A demo whose
// listener is off cannot be connected to, and `roost project doctor
// -workflow player-tcp` would fail on the project it just generated.
func enableDemoPlayerTCP(root, gameService string) error {
	_, err := ensurePlayerTCPConfig(root, gameService, "", true)
	return err
}

func demoScaffoldSteps(gameService string) []demoScaffoldStep {
	return []demoScaffoldStep{
		{add: &AddOptions{Kind: "component", Name: "Profile", Entity: "Player"}, why: "Player's identity state"},
		{add: &AddOptions{Kind: "component", Name: "Bag", Entity: "Player"}, why: "Player's item state"},
		{add: &AddOptions{Kind: "dao", Name: "Player", Entity: "Player"}, why: "persistence for both components"},
		{write: "db/def/player.go", why: "the persistent fields the generated mutators are built from"},
		{write: "game/entities/player/profile_component.go", why: "rename and level-up through generated mutators; level-up emits an effect"},
		{write: "game/entities/player/bag_component.go", why: "add-item: table lookup, coded errors, generated map mutators"},
		{write: "game/entities/player/entity.go", why: "EntityCategoryPlayer: the kind's lock rank, decided before the first document is persisted"},
		{write: "game/effects/level_up.go", why: "the level-up effect: topic, payload, and the Emit onto the current transaction"},
		{add: &AddOptions{Kind: "component", Name: "Stats", Entity: "World"}, why: "World's one job: server-wide counters"},
		{add: &AddOptions{Kind: "dao", Name: "World", Entity: "World"}, why: "persistence for the counters"},
		{write: "db/def/world.go", why: "PlayersEntered and MatchesFormed"},
		{write: "game/entities/world/stats_component.go", why: "count logins and matches through generated mutators; snapshot for reads"},
		{write: "game/entities/world/entity.go", why: "EntityCategoryWorld: locked before Player in the two-entity AddExp"},
		{write: "game/matchmaking/queue.go", why: "the duel queue and how a player is named in it"},
		{add: &AddOptions{Kind: "table", Name: "Item"}, why: "the item config table"},
		{write: "configs/schema/item.go", why: "the table's columns and rules"},
		{write: "configs/table/item.csv", why: "the rows; converted to configs/data/item.json by generate"},
		{add: &AddOptions{Kind: "errcode", Name: "ItemUnknown", ID: 100001}, why: "a coded failure in the manifest's errcode space"},
		{add: &AddOptions{Kind: "errcode", Name: "ItemCount", ID: 100002}, why: "a coded failure in the manifest's errcode space"},
		{add: &AddOptions{Kind: "errcode", Name: "BagFull", ID: 100003}, why: "a coded failure in the manifest's errcode space"},
		{add: &AddOptions{Kind: "errcode", Name: "ExpAmount", ID: 100004}, why: "a coded failure in the manifest's errcode space"},
		{add: &AddOptions{Kind: "errcode", Name: "NameEmpty", ID: 100005}, why: "a coded failure in the manifest's errcode space"},
		{write: "internal/errors/item_unknown.go", why: "client-facing message instead of the TODO placeholder"},
		{write: "internal/errors/item_count.go", why: "client-facing message instead of the TODO placeholder"},
		{write: "internal/errors/bag_full.go", why: "client-facing message instead of the TODO placeholder"},
		{write: "internal/errors/exp_amount.go", why: "client-facing message instead of the TODO placeholder"},
		{write: "internal/errors/name_empty.go", why: "client-facing message instead of the TODO placeholder"},
		{add: &AddOptions{Kind: "handler", Name: "AddItem", Entity: "Player", Component: "Bag"}, why: "the write transaction"},
		{write: "game/handler/add_item.go", why: "handler parameters and result; the Sender and endpoint are generated from them"},
		{add: &AddOptions{Kind: "handler", Name: "AddExp", Entity: "Player", Component: "Profile"}, why: "the transaction that starts the event chain"},
		{write: "game/handler/add_exp.go", why: "two entity parameters (Player, World) in one transaction; the Sender becomes MultiSync_AddExp"},
		{add: &AddOptions{Kind: "handler", Name: "RecordEnter", Entity: "World", Component: "Stats"}, why: "World counts a login"},
		{write: "game/handler/record_enter.go", why: "a separate Nest call after the Player one"},
		{add: &AddOptions{Kind: "handler", Name: "RecordMatch", Entity: "World", Component: "Stats"}, why: "World counts a formed match"},
		{write: "game/handler/record_match.go", why: "called by the matchmaker after the remote Commit"},
		{add: &AddOptions{Kind: "handler", Name: "WorldStats", Entity: "World", Component: "Stats"}, why: "a read under the lock"},
		{write: "game/handler/world_stats.go", why: "returns a value, not the Entity"},
		{add: &AddOptions{Kind: "access", Name: "player", Service: gameService}, why: "the player request boundary"},
		{add: &AddOptions{Kind: "transport", Name: "tcp"}, why: "a transport a client can actually connect to"},
		{write: "internal/access/player/tcp/auth.go", why: "session tickets validated by the account service, plus a terminal shortcut"},
		{run: enableDemoPlayerTCP, why: "a listener that is actually on; doctor's player-tcp workflow passes on the generated project"},
		{add: &AddOptions{Kind: "protocol", Name: "AddItem", Group: "game", Handler: "player"}, why: "the wire message"},
		{write: "protocol/def/add_item.go", why: "request fields matching the handler parameters; response with code and count"},
		{add: &AddOptions{Kind: "endpoint", Name: "AddItem", Handler: "player"}, why: "protocol boundary to Nest sender"},
		{write: "game/controllers/player/add_item.go", why: "the error boundary: coded errors become the response, not a dropped connection"},
		{add: &AddOptions{Kind: "protocol", Name: "AddExp", Group: "game", Handler: "player"}, why: "the wire message"},
		{write: "protocol/def/add_exp.go", why: "request field matching the handler parameter; response with code and levels gained"},
		{write: "game/controllers/player/add_exp.go", why: "hand-written: add endpoint wires single-entity handlers, AddExp addresses two"},
		{add: &AddOptions{Kind: "protocol", Name: "EnterGame", Group: "game", Handler: "player"}, why: "the first message after the handshake"},
		{write: "protocol/def/enter_game.go", why: "no Nest handler behind it: creating a Player is a lifecycle operation"},
		{write: "game/controllers/player/controller.go", why: "the controller also holds the Player lifecycle"},
		{write: "game/controllers/player/enter_game.go", why: "hand-written endpoint: GetOrCreate the Player, then answer"},
		{add: &AddOptions{Kind: "protocol", Name: "JoinQueue", Group: "game", Handler: "player"}, why: "the cross-service call: game → match"},
		{write: "protocol/def/join_queue.go", why: "no Nest handler behind it either: the match service is another process"},
		{write: "game/controllers/player/join_queue.go", why: "Enqueue through the typed match client, frame sequence as idempotency key"},
		{add: &AddOptions{Kind: "protocol", Name: "PollMatch", Group: "game", Handler: "player"}, why: "reading the ticket and the match"},
		{write: "protocol/def/poll_match.go", why: "state, match id, members"},
		{write: "game/controllers/player/poll_match.go", why: "ownership-checked reads through the match client"},
		{add: &AddOptions{Kind: "protocol", Name: "WorldStats", Group: "game", Handler: "player"}, why: "the World's counters"},
		{write: "protocol/def/world_stats.go", why: "two counters"},
		{write: "game/controllers/player/world_stats.go", why: "a Nest read handler on the World"},
		{add: &AddOptions{Kind: "protocol", Name: "MatchFound", Group: "game", Handler: "player"}, why: "the server push announcing a match"},
		{write: "protocol/def/match_found.go", why: "a notify: no request, the bind registers its encoder"},
		{run: enableDemoMatchSweep, why: "the match process sweeps the duel queue for expired tickets"},
		{write: "loadtest/playertcp/conn.go", why: "the robot transport that speaks the generated server's frame"},
		{write: "loadtest/scenarios/demo.yaml", why: "one robot's life, as a scenario tree"},
		{write: "cmd/loadtest/main.go", why: "robots + thresholds: the load test that is also the regression test"},
		{write: "deploy/dev/observability/docker-compose.yaml", why: "Prometheus + Grafana for a developer machine, apart from the regenerated infrastructure compose"},
		{write: "deploy/dev/observability/prometheus.yml", why: "scrape the three ops endpoints and the load test"},
		{write: "deploy/dev/observability/grafana/provisioning/datasources/prometheus.yaml", why: "the provisioned datasource"},
		{write: "deploy/dev/observability/grafana/provisioning/dashboards/dashboards.yaml", why: "load dashboards from the mounted directory"},
		{write: "deploy/dev/observability/grafana/dashboards/roost-demo.json", why: "the dashboard, one row per step of the chain"},
		{write: "deploy/dev/observability/README.md", why: "metric ↔ chain step ↔ what to look at"},
		{write: "internal/service/game/service.go", why: "the game service starts the effect consumer in Init and drains it in Shutdown"},
		{write: "internal/service/game/level_up_mail.go", why: "the consumer: JetStream durable + Mongo inbox → mail.Send keyed by EffectID"},
		{write: "internal/service/game/matchmaker.go", why: "Candidates → Grouping → Commit on a ticker, then the World records the match and the players are pushed MatchFound"},
		{write: "cmd/accountctl/main.go", why: "the operator surface account keeps off the bus: register the game server so CreateRole works"},
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
	vars := demoVars{module: m.Project.Module, gameService: gameService, project: m.Project.Name}
	for _, step := range demoScaffoldSteps(gameService) {
		if step.add != nil {
			files, addErr := Add(root, *step.add)
			if addErr != nil {
				return created, fmt.Errorf("template %s: add %s %s (%s): %w", demoTemplateName, step.add.Kind, step.add.Name, step.why, addErr)
			}
			created = append(created, files...)
			continue
		}
		if step.run != nil {
			if runErr := step.run(root, gameService); runErr != nil {
				return created, fmt.Errorf("template %s: %s: %w", demoTemplateName, step.why, runErr)
			}
			continue
		}
		written, writeErr := writeDemoFile(root, vars, step.write)
		if writeErr != nil {
			return created, fmt.Errorf("template %s: write %s (%s): %w", demoTemplateName, step.write, step.why, writeErr)
		}
		created = append(created, written)
	}
	if err := Generate(root, GenerateOptions{Stdout: io.Discard}); err != nil {
		return created, fmt.Errorf("template %s: regenerate: %w", demoTemplateName, err)
	}
	return created, nil
}
