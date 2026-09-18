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
	for _, feature := range []string{"protocol", "entity", "nest", "dao", "config", "errcode", "attribute"} {
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
	const before, after = "  sweep_queues: []\n", "  sweep_queues:\n    - duel:2:default\n    - ranked:2:default\n"
	if !strings.Contains(string(raw), before) {
		return fmt.Errorf("%s: expected %q to replace", path, strings.TrimSpace(before))
	}
	return writeAtomic(path, []byte(strings.Replace(string(raw), before, after, 1)), 0o644)
}

// enableDemoAdmin turns the ops admin endpoint on in the game service's DEV
// config with a dev token: GET /admin/commands and POST /admin/execute answer
// on ops.addr with X-Admin-Token: dev-gm-token. The production example config
// is untouched — it keeps admin off, and config check --production refuses a
// dev- token anyway, so a real deployment mints its own.
func enableDemoAdmin(root, gameService string) error {
	path := filepath.Join(root, "configs", "service", "config."+gameService+".yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	const before = "  admin_enabled: false\n  admin_token: \"\"\n  allow_dev_token: false\n"
	const after = "  admin_enabled: true\n  admin_token: dev-gm-token\n  allow_dev_token: true\n"
	if !strings.Contains(string(raw), before) {
		return fmt.Errorf("%s: expected the ops admin block to replace", path)
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
		{write: "game/matchmaking/queue.go", why: "the duel and ranked queues, their grouping policies, and how a player is named in them"},
		{add: &AddOptions{Kind: "table", Name: "Item"}, why: "the item config table"},
		{write: "configs/schema/item.go", why: "the table's columns and rules"},
		{write: "configs/table/item.csv", why: "the rows; converted to configs/data/item.json by generate"},
		{add: &AddOptions{Kind: "errcode", Name: "ItemUnknown", ID: 100001}, why: "a coded failure in the manifest's errcode space"},
		{add: &AddOptions{Kind: "errcode", Name: "ItemCount", ID: 100002}, why: "a coded failure in the manifest's errcode space"},
		{add: &AddOptions{Kind: "errcode", Name: "BagFull", ID: 100003}, why: "a coded failure in the manifest's errcode space"},
		{add: &AddOptions{Kind: "errcode", Name: "ExpAmount", ID: 100004}, why: "a coded failure in the manifest's errcode space"},
		{add: &AddOptions{Kind: "errcode", Name: "NameEmpty", ID: 100005}, why: "a coded failure in the manifest's errcode space"},
		{add: &AddOptions{Kind: "errcode", Name: "ItemShort", ID: 100006}, why: "a coded failure in the manifest's errcode space"},
		{add: &AddOptions{Kind: "errcode", Name: "BattleNotFound", ID: 100007}, why: "a coded failure in the manifest's errcode space"},
		{add: &AddOptions{Kind: "errcode", Name: "BattleNotSeated", ID: 100008}, why: "a coded failure in the manifest's errcode space"},
		{add: &AddOptions{Kind: "errcode", Name: "BattleBusy", ID: 100009}, why: "a coded failure in the manifest's errcode space"},
		{add: &AddOptions{Kind: "errcode", Name: "DungeonRun", ID: 100010}, why: "a coded failure in the manifest's errcode space"},
		{add: &AddOptions{Kind: "errcode", Name: "MailClaim", ID: 100011}, why: "a coded failure in the manifest's errcode space"},
		{write: "internal/errors/item_unknown.go", why: "client-facing message instead of the TODO placeholder"},
		{write: "internal/errors/item_count.go", why: "client-facing message instead of the TODO placeholder"},
		{write: "internal/errors/bag_full.go", why: "client-facing message instead of the TODO placeholder"},
		{write: "internal/errors/exp_amount.go", why: "client-facing message instead of the TODO placeholder"},
		{write: "internal/errors/name_empty.go", why: "client-facing message instead of the TODO placeholder"},
		{write: "internal/errors/item_short.go", why: "client-facing message instead of the TODO placeholder"},
		{write: "internal/errors/battle_not_found.go", why: "client-facing message instead of the TODO placeholder"},
		{write: "internal/errors/battle_not_seated.go", why: "client-facing message instead of the TODO placeholder"},
		{write: "internal/errors/battle_busy.go", why: "client-facing message instead of the TODO placeholder"},
		{write: "internal/errors/dungeon_run.go", why: "client-facing message instead of the TODO placeholder"},
		{write: "internal/errors/mail_claim.go", why: "client-facing message instead of the TODO placeholder"},
		{write: "internal/errors/scene_position.go", why: "one code for out of bounds / taken / not on a map: a client that learns which points are occupied has everyone's positions"},
		{write: "internal/errors/dungeon_claim_window.go", why: "a reward refused for being too old has to be a named refusal, not a quiet zero"},
		{write: "game/gameplay/attribute/combat.go", why: "the attribute profile: three attributes, one derived by formula, plus the dirty mask the generator writes through"},
		{write: "game/gameplay/attribute/combat_test.go", why: "the derived attribute follows its inputs and a container snapshot is a copy"},
		{write: "game/entities/player/map_component.go", why: "where the player is: the only door to the position, read and write both landing on the DAO"},
		{write: "game/entities/player/attribute_component.go", why: "the attribute container wired into an Entity: layers decided, composed, persisted and replicated"},
		{write: "game/entities/player/attribute_component_test.go", why: "the composition rule as a test: Final = Base + Gear with the derived attribute computed once over the composed inputs"},
		{write: "game/entities/player/sync_packer.go", why: "the client-facing half of sync=true: who serializes a subject, by profile and by dirty mask"},
		{add: &AddOptions{Kind: "entity", Name: "Scene"}, why: "the map is an Entity: an id, a lifecycle the framework drives, addressable by GM and by another process later"},
		{add: &AddOptions{Kind: "lifecycle", Name: "Scene", Service: gameService}, why: "the create/load boundary for scenes"},
		{write: "game/scene/scene.go", why: "the contract between the Scene Entity and its systems: the map's shape, and what a system promises"},
		{write: "game/scene/runtime/runtime.go", why: "the scene runtime as a composition of named systems, started in order and stopped in reverse"},
		{write: "game/scene/runtime/terrain.go", why: "the map: bounds, walkability and occupancy, with its own lock"},
		{write: "game/scene/runtime/pathfind.go", why: "routing and placement: bounded A*, and the nearest free point searched in rings"},
		{write: "game/scene/runtime/runtime_test.go", why: "the map's promises: edges, occupancy, the nearest free point, no route through a wall, and systems that answer after they stop"},
		{write: "game/scene/runtime/relations.go", why: "a set-driven interest source: the shape every social relationship shares, fed by whoever owns the relationship"},
		{write: "game/scene/runtime/interest.go", why: "the AOI plus the relation sources, aggregated: the first source subscribes, the last one unsubscribes"},
		{write: "game/scene/runtime/interest_test.go", why: "the promises the AOI batch adds: self through a relation, hysteresis that does not churn, and a relation outliving the distance that also held the pair"},
		{write: "game/entities/scene/entity.go", why: "the map as an Entity: no DAO, rebuilt from configuration, exporting its systems by interface"},
		{write: "game/lifecycle/scene_singleton.go", why: "the one map this process runs, built from configuration before anyone can stand on it"},
		{write: "game/ranking/ranking.go", why: "the game's side of the rank service: which board, what a point is, and the run id that makes a submit idempotent"},
		{write: "game/dungeon/dungeon.go", why: "what a clear is worth and why paying for one exactly once needs a claim ledger, not the request's own flag"},
		{write: "game/dungeon/dungeon_test.go", why: "the claim window as a table test, shipped with the project"},
		{write: "game/battle/battle.go", why: "the lockstep contract: tick rate, frame budget, input encoding, seats and the deterministic simulation both clients run"},
		{add: &AddOptions{Kind: "saga", Name: "GiftItem", Service: gameService, Steps: []string{"debit", "deliver"}}, why: "the gift saga's definition and step subscriptions; the saga mod joins the game service"},
		{write: "game/gift/gift.go", why: "the game's side of the gift: state, id, mail text"},
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
		{add: &AddOptions{Kind: "handler", Name: "PlayerLevel", Entity: "Player", Component: "Profile"}, why: "the ranked queue's score, read under the Player's lock"},
		{write: "game/handler/player_level.go", why: "a read handler on the Player"},
		{add: &AddOptions{Kind: "handler", Name: "StartGift", Entity: "Player", Component: "Bag"}, why: "the saga start: check under the lock, emit the start intent in the same WAL record"},
		{write: "game/handler/start_gift.go", why: "no mutation, one effect: saga.EmitStart"},
		{add: &AddOptions{Kind: "handler", Name: "GiftDebit", Entity: "Player", Component: "Bag"}, why: "the saga's debit step as a Nest transaction"},
		{write: "game/handler/gift_debit.go", why: "RemoveItem plus the command's receipt and completion, all in one WAL record"},
		{add: &AddOptions{Kind: "handler", Name: "GiftRefund", Entity: "Player", Component: "Bag"}, why: "debit's compensation, with the same saga identity"},
		{write: "game/handler/gift_refund.go", why: "AddItem plus the receipt and completion; a compensation that runs twice is as wrong as a step that does"},
		{add: &AddOptions{Kind: "handler", Name: "ClaimDungeon", Entity: "Player", Component: "Profile"}, why: "the clear reward, paid at most once per run"},
		{write: "game/handler/claim_dungeon.go", why: "the claim ledger and the reward commit together, so a replay pays nothing"},
		{add: &AddOptions{Kind: "handler", Name: "ClaimMailReward", Entity: "Player", Component: "Bag"}, why: "the mail attachment, granted at most once per mail"},
		{write: "game/handler/claim_mail_reward.go", why: "the same ledger shape for the claim the mail service cannot make atomic"},
		{write: "game/handler/claim_mail_reward_test.go", why: "the mail ledger asserted through the real handler in a real Nest transaction: the crash window a client cannot open"},
		{write: "game/handler/claim_dungeon_test.go", why: "the clear reward's ledger and its window asserted together: a pruned run is refused, not paid again"},
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
		{write: "game/chatroom/chatroom.go", why: "the game's side of the chat contract: channels, the text type, who is online"},
		{add: &AddOptions{Kind: "protocol", Name: "SendChat", Group: "game", Handler: "player"}, why: "the cross-service call: game → chat"},
		{write: "protocol/def/send_chat.go", why: "kind, target, text; the stored sequence comes back"},
		{write: "game/controllers/player/send_chat.go", why: "Publish through the typed chat client, then fan the stored line out"},
		{add: &AddOptions{Kind: "protocol", Name: "ChatHistory", Group: "game", Handler: "player"}, why: "the reconnect path: page a channel forwards"},
		{write: "protocol/def/chat_history.go", why: "cursor in, lines and cursor out"},
		{write: "game/controllers/player/chat_history.go", why: "History through the typed chat client, viewer checked by the service"},
		{add: &AddOptions{Kind: "protocol", Name: "ChatMessage", Group: "game", Handler: "player"}, why: "the server push carrying one chat line"},
		{write: "protocol/def/chat_message.go", why: "a notify with the same shape history returns"},
		{write: "game/controllers/player/chat_push.go", why: "Message → push, and who receives it: presence for world, the pair for private"},
		{write: "game/rewards/rewards.go", why: "what a mail attachment means to this game; shared by the mailer and the claim endpoint"},
		{add: &AddOptions{Kind: "protocol", Name: "ListMail", Group: "game", Handler: "player"}, why: "the mailbox, paged"},
		{write: "protocol/def/list_mail.go", why: "cursor in, envelopes with this player's state out"},
		{write: "game/controllers/player/list_mail.go", why: "List through the typed mail client"},
		{add: &AddOptions{Kind: "protocol", Name: "ClaimMail", Group: "game", Handler: "player"}, why: "the attachment into the Bag"},
		{write: "protocol/def/claim_mail.go", why: "mail id in, what was granted out"},
		{write: "game/controllers/player/claim_mail.go", why: "ReserveClaim → Sync_AddItem → CommitClaim: exactly-once delivery around one Nest transaction"},
		{add: &AddOptions{Kind: "protocol", Name: "EnterDungeon", Group: "game", Handler: "player"}, why: "the cross-service call: game → session"},
		{write: "protocol/def/enter_dungeon.go", why: "a bounded run: id, state, deadline"},
		{write: "game/controllers/player/enter_dungeon.go", why: "Enter through the typed session client, frame sequence as idempotency key"},
		{add: &AddOptions{Kind: "protocol", Name: "FinishDungeon", Group: "game", Handler: "player"}, why: "resolving the run"},
		{write: "protocol/def/finish_dungeon.go", why: "the verdict in, terminal state and the clear's exp out"},
		{write: "game/controllers/player/finish_dungeon.go", why: "Finish through the session client, then the clear's exp through the AddExp transaction"},
		{add: &AddOptions{Kind: "handler", Name: "EnterScene", Entity: "Player", Component: "Map"}, why: "placing a player: the scene resolves the point, the player writes it"},
		{write: "game/handler/enter_scene.go", why: "a remembered position that is no longer usable becomes the nearest free one, not a failed login"},
		{add: &AddOptions{Kind: "handler", Name: "PlayerPosition", Entity: "Player", Component: "Map"}, why: "a refused move answers with where the player still is; there is no cached copy to read instead"},
		{write: "game/handler/player_position.go", why: "the read, under the Player's lock"},
		{add: &AddOptions{Kind: "handler", Name: "MovePlayer", Entity: "Player", Component: "Map"}, why: "moving holds the Scene and the Player together: the stored position and the ground handed out have to agree"},
		{write: "game/handler/move_player.go", why: "the two-entity move; the terrain check stays in the component that owns the write"},
		{add: &AddOptions{Kind: "protocol", Name: "Move", Group: "game", Handler: "player"}, why: "the movement endpoint"},
		{write: "protocol/def/move.go", why: "ask for a point, get back the one the server settled on"},
		{write: "game/controllers/player/move.go", why: "resolve the scene, then the move transaction"},
		{add: &AddOptions{Kind: "protocol", Name: "RankTop", Group: "game", Handler: "player"}, why: "reading a leaderboard: a bounded page, and the board is the server's choice"},
		{write: "protocol/def/rank_top.go", why: "the board page on the wire, with this player's own rank alongside it"},
		{write: "game/controllers/player/rank_top.go", why: "Page + Rank through the typed rank client"},
		{add: &AddOptions{Kind: "skill", Name: "Fireball"}, why: "one skill definition and the embedded catalog"},
		{write: "game/skills/fireball.json", why: "the definition with its contract written down; compiled at startup"},
		{add: &AddOptions{Kind: "protocol", Name: "SkillCatalog", Group: "game", Handler: "player"}, why: "what the server compiled"},
		{write: "protocol/def/skill_catalog.go", why: "skill ids and the warning count"},
		{write: "game/controllers/player/skill_catalog.go", why: "compile once, list the programs"},
		{add: &AddOptions{Kind: "protocol", Name: "SendGift", Group: "game", Handler: "player"}, why: "the saga start from a client"},
		{write: "protocol/def/send_gift.go", why: "recipient, item, count in; the saga id out"},
		{write: "game/controllers/player/send_gift.go", why: "StartGift through the Nest sender; the saga id is sender + session + frame sequence"},
		{add: &AddOptions{Kind: "protocol", Name: "BattleInput", Group: "game", Handler: "player"}, why: "one client's contribution to one lockstep frame"},
		{write: "protocol/def/battle_input.go", why: "input, keyframe hash and catch-up request on one per-frame message"},
		{write: "game/controllers/player/battle_input.go", why: "hands the message to the battle room's goroutine; no Entity is locked"},
		{add: &AddOptions{Kind: "protocol", Name: "BattleFrame", Group: "game", Handler: "player"}, why: "the server push carrying one lockstep broadcast packet"},
		{write: "protocol/def/battle_frame.go", why: "a notify: the packet the room encoded, redundancy included"},
		{write: "protocol/def/entity_sync.go", why: "the scene's state frames on the wire: reliable snapshots and fragmented deltas in one push"},
		{add: &AddOptions{Kind: "protocol", Name: "GiftStatus", Group: "game", Handler: "player"}, why: "how the saga went"},
		{write: "protocol/def/gift_status.go", why: "the coordinator's status, step and last error"},
		{write: "game/controllers/player/gift_status.go", why: "Engine.Get through the saga capability; unknown until the start has been consumed"},
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
		{write: "internal/service/game/battle_test.go", why: "the room's start grace and lifetime, asserted against the real lockstep room with a recording push lane"},
		{write: "internal/service/game/battle.go", why: "the lockstep rooms: one goroutine owns each room, the player TCP push is its broadcast lane, the matchmaker opens one per match"},
		{write: "internal/service/game/scene.go", why: "the other half of sync=true: a room that holds every online player as a subject and pushes their deltas to the others, gated on the Data Engine's durable watermark"},
		{write: "internal/service/game/scene_test.go", why: "the replication claim end to end: one player's DAO change decodes as a delta on another player's wire"},
		{write: "internal/service/game/gift_saga.go", why: "the gift saga's four step consumers: debit / refund as Nest transactions, deliver as a mail, each idempotent per command through the Mongo step inbox"},
		{write: "internal/service/game/gm.go", why: "GM commands on the admin registry: add item / add exp / send mail / world stats, served by ops over HTTP behind a token"},
		{run: enableDemoAdmin, why: "the dev config enables the ops admin endpoint with a dev token, so the GM commands are reachable on a developer machine"},
		{write: "cmd/accountctl/main.go", why: "the operator surface account keeps off the bus: register the game server so CreateRole works"},
		{write: "internal/service/account/collaborators.go", why: "an account service that can log a demo user in and mint ids from Redis"},
		{write: "internal/service/chat/collaborators.go", why: "a chat service with a written-down policy, one text type and a granted system path"},
		{write: "internal/service/session/collaborators.go", why: "a session service whose releaser frees the demo's (resource-less) dungeon"},
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
