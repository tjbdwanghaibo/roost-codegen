package roost

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The embed pattern in demo/embed.go lists top-level directories by name, so a
// new one that nobody adds to it would ship as a silently missing template.
// Compare the embedded set against the directory on disk.
func TestDemoEmbedCoversEveryFile(t *testing.T) {
	embedded, err := demoSourceFiles()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(embedded)

	root := filepath.Join("..", "..", "demo")
	var onDisk []string
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".tmpl") {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		onDisk = append(onDisk, strings.TrimSuffix(filepath.ToSlash(rel), ".tmpl"))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(onDisk)
	if strings.Join(embedded, "\n") != strings.Join(onDisk, "\n") {
		t.Fatalf("demo/embed.go does not embed every template.\nembedded:\n%s\non disk:\n%s",
			strings.Join(embedded, "\n"), strings.Join(onDisk, "\n"))
	}
	if len(onDisk) == 0 {
		t.Fatal("no demo templates found; the walk or the layout is wrong")
	}
}

// Every shipped template must be written by a step, and every write step must
// name a shipped template: a template nothing writes is dead weight, and a
// step naming a missing file fails only at generation time.
func TestDemoTemplateStepsAndShippedFilesAgree(t *testing.T) {
	shipped, err := demoSourceFiles()
	if err != nil {
		t.Fatal(err)
	}
	written := map[string]bool{}
	for _, step := range demoScaffoldSteps("game") {
		if step.add != nil || step.run != nil {
			continue
		}
		if step.write == "" {
			t.Fatal("a step neither adds, runs nor writes")
		}
		if written[step.write] {
			t.Errorf("step writes %s twice", step.write)
		}
		written[step.write] = true
	}
	for _, file := range shipped {
		if !written[file] {
			t.Errorf("template %s is shipped but no step writes it", file)
		}
		delete(written, file)
	}
	for file := range written {
		t.Errorf("a step writes %s but no such template is shipped", file)
	}
}

// The demo builds on the game template and adds what its own code needs from
// the manifest, so a caller who narrowed -features still gets a project that
// compiles.
func TestDemoTemplateKeepsTheGameTemplateAndItsFeatures(t *testing.T) {
	m := DefaultManifest("planet", "example.com/planet", []string{"game"}, []string{"configdata"}, []string{"config"})
	if err := applyDemoTemplate(&m, "game"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"account", "mail", "match", "chat", "session"} {
		if m.Services[name].Framework != name {
			t.Errorf("demo dropped hosted service %s: %+v", name, m.Services[name])
		}
	}
	for _, feature := range []string{"protocol", "entity", "nest", "dao", "config"} {
		if !contains(m.Features, feature) {
			t.Errorf("feature %q missing: %v", feature, m.Features)
		}
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
}

// The whole template, generated into a real directory: the demo's own files
// land, they are valid Go, and the pieces the generators read out of them —
// the handler's parameters and the request fields that must match — are
// present. Compilation against roost-core is verified by CI, which generates a
// project from this template and builds it.
func TestDemoTemplateGeneratesABuildableWritePath(t *testing.T) {
	target := filepath.Join(t.TempDir(), "planet")
	if _, _, err := NewProject(NewOptions{
		Name: "planet", Module: "example.com/planet", Out: target,
		Mods: []string{"configdata", "mongo", "nats", "dataengine", "nest"}, Template: demoTemplateName,
	}); err != nil {
		t.Fatal(err)
	}

	shipped, err := demoSourceFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range shipped {
		path := filepath.Join(target, filepath.FromSlash(rel))
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("demo file %s missing: %v", rel, readErr)
		}
		if strings.Contains(string(raw), demoModulePlaceholder) {
			t.Errorf("%s still contains the module placeholder", rel)
		}
		if !strings.HasSuffix(rel, ".go") {
			continue
		}
		if _, parseErr := parser.ParseFile(token.NewFileSet(), path, raw, parser.AllErrors); parseErr != nil {
			t.Errorf("%s does not parse: %v", rel, parseErr)
		}
	}

	read := func(rel string) string {
		t.Helper()
		raw, readErr := os.ReadFile(filepath.Join(target, filepath.FromSlash(rel)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		return string(raw)
	}
	// The endpoint generator matches handler parameters against request
	// fields, so these two must stay in step; they are the demo's contract.
	if handler := read("game/handler/add_item.go"); !strings.Contains(handler, "itemID int64, count int32") {
		t.Errorf("handler lost its parameters:\n%s", handler)
	}
	if request := read("protocol/def/add_item.go"); !strings.Contains(request, "ItemID int64") || !strings.Contains(request, "Count  int32") {
		t.Errorf("request fields do not match the handler parameters:\n%s", request)
	}
	// The generated endpoint is what proves the two were wired together.
	if endpoint := read("game/controllers/player/add_item.go"); !strings.Contains(endpoint, "request.ItemID") || !strings.Contains(endpoint, "request.Count") {
		t.Errorf("endpoint does not pass the request through:\n%s", endpoint)
	}
	// The demo replaces the fail-closed skeleton with a session-ticket
	// authenticator and ships no debug credential: a token the server does
	// not verify is not a credential.
	if auth := read("internal/access/player/tcp/auth.go"); !strings.Contains(auth, sessionTokenPrefixName) || strings.Contains(auth, `"player:"`) {
		t.Errorf("auth.go is not the session-ticket authenticator, or still carries the debug shortcut:\n%s", auth)
	}
	// Persistent state must go through generated mutators, not raw fields,
	// and validation reads the item table through the generated accessor.
	if bag := read("game/entities/player/bag_component.go"); !strings.Contains(bag, "dao.SetItems(") || !strings.Contains(bag, "generated.ItemByID(") {
		t.Errorf("bag component does not use the generated mutator and table accessor:\n%s", bag)
	}
	// The demo endpoint replaces the generated scaffold with the error
	// boundary; a handler error that reached the access layer would close the
	// connection.
	if endpoint := read("game/controllers/player/add_item.go"); !strings.Contains(endpoint, "errcode.ClientError(err)") {
		t.Errorf("endpoint does not translate errors at the boundary:\n%s", endpoint)
	}
	// The table exists in all three forms the generator maintains: schema,
	// rows, and the JSON the runtime loads — the last proves the final
	// Generate ran the CSV conversion on the demo's rows.
	if schema := read("configs/schema/item.go"); !strings.Contains(schema, "//roost:table name=item key=ID") {
		t.Errorf("item schema lost its marker:\n%s", schema)
	}
	if data := read("configs/data/item.json"); !strings.Contains(data, "\"id\": 1001") {
		t.Errorf("item.json was not converted from the demo rows:\n%s", data)
	}
	// Coded errors sit in the manifest's errcode space, so `roost id check`
	// owns their uniqueness; an id outside it would fail at add time.
	for _, file := range []string{"internal/errors/item_unknown.go", "internal/errors/item_count.go", "internal/errors/bag_full.go"} {
		if body := read(file); !strings.Contains(body, "errcode.Define(1000") || strings.Contains(body, "TODO") {
			t.Errorf("%s is not a finished coded error:\n%s", file, body)
		}
	}
	// The account collaborators must be the working ones, bound to Redis
	// through the kit hook; the game template's defaults refuse every login.
	// Every hosted service's collaborators are implemented: doctor judges by
	// the stub marker, so the demo's own texts must not contain it either
	// (account's verifier once said "channel %q is not configured" and read as
	// a stub).
	for _, service := range []string{"account", "chat", "mail", "match", "session"} {
		if collaborators := read("internal/service/" + service + "/collaborators.go"); strings.Contains(collaborators, collaboratorUnconfiguredMarker) {
			t.Errorf("%s collaborators still read as unconfigured to doctor:\n%s", service, collaborators)
		}
	}
	if chatCollaborators := read("internal/service/chat/collaborators.go"); !strings.Contains(chatCollaborators, "chat.GrantSystem()") || !strings.Contains(chatCollaborators, "chatroom.TypeText") {
		t.Errorf("chat collaborators do not grant the system path and register the text type:\n%s", chatCollaborators)
	}
	for _, rel := range []string{"game/chatroom/chatroom.go", "protocol/def/send_chat.go", "protocol/def/chat_history.go", "protocol/def/chat_message.go",
		"game/controllers/player/send_chat.go", "game/controllers/player/chat_history.go", "game/controllers/player/chat_push.go", "deploy/dev/run.sh",
		"game/rewards/rewards.go", "protocol/def/list_mail.go", "protocol/def/claim_mail.go",
		"game/controllers/player/list_mail.go", "game/controllers/player/claim_mail.go",
		"internal/service/session/collaborators.go", "protocol/def/enter_dungeon.go", "protocol/def/finish_dungeon.go", "game/handler/player_level.go",
		"game/controllers/player/enter_dungeon.go", "game/controllers/player/finish_dungeon.go",
		"game/skills/fireball.json", "game/skills/catalog.go", "protocol/def/skill_catalog.go", "game/controllers/player/skill_catalog.go"} {
		if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(rel))); err != nil {
			t.Errorf("demo did not write %s: %v", rel, err)
		}
	}
	if mailer := read("internal/service/game/level_up_mail.go"); !strings.Contains(mailer, "rewards.Encode(rewards.LevelUpReward(") {
		t.Errorf("the level-up mail carries no reward attachment, so ClaimMail has nothing to claim")
	}
	if claim := read("game/controllers/player/claim_mail.go"); !strings.Contains(claim, ".ReserveClaim(") || !strings.Contains(claim, ".Sync_AddItem(") || !strings.Contains(claim, ".CommitClaim(") {
		t.Errorf("claim_mail does not run the reserve → grant → commit sequence")
	}
	if conn := read("loadtest/playertcp/conn.go"); !strings.Contains(conn, "header[3]&flagServerPush == 0 && wire != 0") {
		t.Errorf("the robot transport does not classify frames by the server-push flag; a push carrying a pending wire sequence would be taken for the response")
	}
	// Five processes on one machine: each config has its own ops port, in the
	// order run.sh and prometheus.yml assume.
	for service, port := range map[string]string{"game": "9100", "account": "9101", "chat": "9102", "mail": "9103", "match": "9104", "session": "9105"} {
		if cfg := read("configs/service/config." + service + ".yaml"); !strings.Contains(cfg, "addr: 127.0.0.1:"+port) {
			t.Errorf("config.%s.yaml does not listen ops on %s", service, port)
		}
	}
	if scrape := read("deploy/dev/observability/prometheus.yml"); !strings.Contains(scrape, "job_name: chat") || !strings.Contains(scrape, ":9102") {
		t.Errorf("prometheus.yml does not scrape the chat process on its assigned port")
	}
	if collaborators := read("internal/service/account/collaborators.go"); !strings.Contains(collaborators, "account.RegistryBound") || strings.Contains(collaborators, "is not configured; implement") {
		t.Errorf("account collaborators are still the refusing defaults:\n%s", collaborators)
	}
	// The authenticator validates session tickets through the account client
	// it is bound to in Provide; the generated transport must expose the hook.
	if auth := read("internal/access/player/tcp/auth.go"); !strings.Contains(auth, "ValidateSession(") || !strings.Contains(auth, "BindRegistry(") {
		t.Errorf("auth.go does not validate sessions through a bound account client:\n%s", auth)
	}
	if server := read("internal/access/player/tcp/server_gen.go"); !strings.Contains(server, "type RegistryBound interface") {
		t.Errorf("the generated transport lost the RegistryBound hook the demo authenticator relies on")
	}
	// The event chain: the component emits on the transaction, the service
	// consumes with an inbox and sends mail keyed by the effect id.
	if profile := read("game/entities/player/profile_component.go"); !strings.Contains(profile, "effects.EmitPlayerLevelUp(") {
		t.Errorf("profile component does not emit the level-up effect:\n%s", profile)
	}
	if service := read("internal/service/game/service.go"); !strings.Contains(service, "startLevelUpMailer(") {
		t.Errorf("game service does not start the effect consumer:\n%s", service)
	}
	if mailer := read("internal/service/game/level_up_mail.go"); !strings.Contains(mailer, "nestwal.SubscribeJetStreamEffects(") || !strings.Contains(mailer, "RequestID:        envelope.EffectID") {
		t.Errorf("level-up mailer is not the inbox-backed, idempotent consumer:\n%s", mailer)
	}
	if endpoint := read("game/controllers/player/add_exp.go"); !strings.Contains(endpoint, "errcode.ClientError(err)") {
		t.Errorf("add_exp endpoint does not translate errors at the boundary:\n%s", endpoint)
	}
	// The load test: the controller can create Players, the EnterGame message
	// is bound, the transport adapter and the scenario ship, and the command
	// checks every response code so a coded failure fails the run.
	if controller := read("game/controllers/player/controller.go"); !strings.Contains(controller, "lifecycle.PlayerFromRegistry(") {
		t.Errorf("controller does not hold the Player lifecycle:\n%s", controller)
	}
	if bind := read("game/protocol_handlers/player/protocol_gen.go"); !strings.Contains(bind, "HandleEnterGame") {
		t.Errorf("EnterGame is not bound to the controller:\n%s", bind)
	}
	if conn := read("loadtest/playertcp/conn.go"); !strings.Contains(conn, "transport.RegisterDialer(") {
		t.Errorf("playertcp does not register a robot dialer:\n%s", conn)
	}
	if spec := read("loadtest/scenarios/demo.yaml"); !strings.Contains(spec, "action: enter_game") || !strings.Contains(spec, "action: add_exp") {
		t.Errorf("demo scenario lost a step:\n%s", spec)
	}
	if command := read("cmd/loadtest/main.go"); !strings.Contains(command, "loadtest.Threshold{") || !strings.Contains(command, "coded(resp.Code, resp.Reason)") {
		t.Errorf("loadtest command lacks thresholds or response-code checks:\n%s", command)
	}
	// Cross-service matchmaking and the World's job: the matchmaker calls the
	// typed match client and applies a Grouping; the World's counters move
	// through Nest handlers; the match process sweeps the demo queue.
	if matchmaker := read("internal/service/game/matchmaker.go"); !strings.Contains(matchmaker, "matcher.Commit(") || !strings.Contains(matchmaker, "grouping.Group(") || !strings.Contains(matchmaker, "Sync_RecordMatch(") {
		t.Errorf("matchmaker does not drive the match service and record into the World:\n%s", matchmaker)
	}
	if stats := read("game/handler/world_stats.go"); !strings.Contains(stats, "(world.Stats, error)") {
		t.Errorf("world_stats handler does not return the counters as a value:\n%s", stats)
	}
	if config := read("configs/service/config.match.yaml"); !strings.Contains(config, "- duel:2:default") {
		t.Errorf("match config does not sweep the duel queue:\n%s", config)
	}
	for _, name := range []string{"HandleJoinQueue", "HandlePollMatch", "HandleWorldStats"} {
		if bind := read("game/protocol_handlers/player/protocol_gen.go"); !strings.Contains(bind, name) {
			t.Errorf("%s is not bound to the controller", name)
		}
	}
	if spec := read("loadtest/scenarios/demo.yaml"); !strings.Contains(spec, "action: join_queue") || !strings.Contains(spec, "action: poll_match") || !strings.Contains(spec, "action: world_stats") {
		t.Errorf("demo scenario lacks the matchmaking steps:\n%s", spec)
	}
	// Push, real login, server registration: the matchmaker pushes MatchFound
	// through the transport Runtime and the bind registers the push encoder;
	// the load test can log in through the account service; the game
	// announces its server id to account.
	if matchmaker := read("internal/service/game/matchmaker.go"); !strings.Contains(matchmaker, "transport.PushPlayer(") || !strings.Contains(matchmaker, "msgid.MsgMatchFound") {
		t.Errorf("matchmaker does not push MatchFound:\n%s", matchmaker)
	}
	if bootstrap := read("game/protocol_bootstrap/protocol_gen.go"); !strings.Contains(bootstrap, "RegisterMatchFoundEncoder(") {
		t.Errorf("the push encoder is not registered by the generated bootstrap:\n%s", bootstrap)
	}
	if spec := read("loadtest/scenarios/demo.yaml"); !strings.Contains(spec, "action: wait_push") || !strings.Contains(spec, "msg: 10100") {
		t.Errorf("scenario does not wait for the MatchFound push:\n%s", spec)
	}
	if command := read("cmd/loadtest/main.go"); !strings.Contains(command, "svcaccount.NewBusClient(") || !strings.Contains(command, `"session:%d:%s"`) || !strings.Contains(command, ".SelectRole(") || strings.Contains(command, `"player:"`) {
		t.Errorf("loadtest does not log in through the account service only:\n%s", command)
	}
	// Lock ranks and the two-entity transaction: both kinds carry their
	// category on the marker, and the generated Sender for AddExp takes one
	// id per entity parameter.
	if entityFile := read("game/entities/player/entity.go"); !strings.Contains(entityFile, "category=entity.EntityCategoryPlayer") {
		t.Errorf("Player is not in EntityCategoryPlayer:\n%s", entityFile)
	}
	if entityFile := read("game/entities/world/entity.go"); !strings.Contains(entityFile, "category=entity.EntityCategoryWorld") {
		t.Errorf("World is not in EntityCategoryWorld:\n%s", entityFile)
	}
	if sender := read("game/handler/syncsender/add_exp_nest_gen.go"); !strings.Contains(sender, "MultiSync_AddExp(ctx context.Context, target int64, stats int64, amount int64)") {
		t.Errorf("AddExp Sender is not the two-entity form:\n%s", sender)
	}
	if operator := read("cmd/accountctl/main.go"); !strings.Contains(operator, ".UpsertServer(") || !strings.Contains(operator, `"roost:planet:account"`) {
		t.Errorf("accountctl does not register a server on the project's account store:\n%s", operator)
	}
	// Observability: the dashboard is valid JSON with panels that all query
	// the provisioned datasource, and the scrape config covers the three
	// processes and the load test.
	var dashboard struct {
		UID    string `json:"uid"`
		Panels []struct {
			Type    string `json:"type"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal([]byte(read("deploy/dev/observability/grafana/dashboards/roost-demo.json")), &dashboard); err != nil {
		t.Fatalf("dashboard is not valid JSON: %v", err)
	}
	queries := 0
	for _, panel := range dashboard.Panels {
		if panel.Type == "row" {
			continue
		}
		if len(panel.Targets) == 0 {
			t.Errorf("dashboard panel of type %s has no query", panel.Type)
		}
		for _, target := range panel.Targets {
			if strings.TrimSpace(target.Expr) == "" {
				t.Errorf("dashboard has an empty query")
			}
			if strings.Contains(target.Expr, demoGameServicePlaceholder) {
				t.Errorf("dashboard query kept the service placeholder: %s", target.Expr)
			}
			queries++
		}
	}
	if dashboard.UID != "roost-demo" || queries < 20 {
		t.Errorf("dashboard uid=%q queries=%d", dashboard.UID, queries)
	}
	if scrape := read("deploy/dev/observability/prometheus.yml"); !strings.Contains(scrape, "job_name: game") || !strings.Contains(scrape, ":9300") {
		t.Errorf("prometheus.yml does not scrape the game process and the load test:\n%s", scrape)
	}
	if command := read("cmd/loadtest/main.go"); !strings.Contains(command, "metrics.PrometheusText(metrics.Snapshot())") {
		t.Errorf("loadtest does not expose its metrics")
	}
	// The whole player-tcp workflow, as doctor judges it: access declared,
	// transport generated, authenticator real, listener enabled.
	manifest, err := LoadManifest(target)
	if err != nil {
		t.Fatal(err)
	}
	items, err := checkPlayerTCPWorkflow(target, manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Status != StatusOK {
			t.Errorf("doctor %s: %s", item.Name, item.Detail)
		}
	}
}

// The demo's service files follow the game service's name: a project whose
// first service is not called "game" gets them under its own directory, in
// its own package, naming itself correctly.
func TestDemoTemplateFollowsTheGameServiceName(t *testing.T) {
	target := filepath.Join(t.TempDir(), "planet")
	if _, _, err := NewProject(NewOptions{
		Name: "planet", Module: "example.com/planet", Out: target, Services: []string{"arena"},
		Mods: []string{"configdata", "mongo", "nats", "dataengine", "nest"}, Template: demoTemplateName,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "internal", "service", "game")); !os.IsNotExist(err) {
		t.Fatalf("internal/service/game exists in a project whose game service is arena (err=%v)", err)
	}
	raw, err := os.ReadFile(filepath.Join(target, "internal", "service", "arena", "level_up_mail.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"package " + safeIdent("arena"), `"arena-level-up-mail"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("level_up_mail.go lacks %q:\n%s", want, raw)
		}
	}
	if strings.Contains(string(raw), demoGameServicePlaceholder) || strings.Contains(string(raw), demoGameServicePackagePlaceholder) {
		t.Errorf("a placeholder survived:\n%s", raw)
	}
	service, err := os.ReadFile(filepath.Join(target, "internal", "service", "arena", "service.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(service), `app.ServiceName("arena")`) {
		t.Errorf("service.go does not name the arena service:\n%s", service)
	}
}

// sessionTokenPrefixName is the identifier the demo authenticator declares.
// The test asserts on the name rather than the literal so renaming the
// credential format does not silently pass.
const sessionTokenPrefixName = "sessionTokenPrefix"

// Every generated Makefile carries a loadtest target. The target guards on
// cmd/loadtest existing, so the game template — which ships no load test —
// keeps the same Makefile and tells the developer what to generate instead.
func TestGeneratedMakefileHasLoadtestTarget(t *testing.T) {
	m := DefaultManifest("planet", "example.com/planet", []string{"game"}, nil, nil)
	plan, err := renderProject(m)
	if err != nil {
		t.Fatal(err)
	}
	body := string(plan["Makefile"].Body)
	for _, want := range []string{"\nloadtest:\n", "LOADTEST_ENDPOINT ?= ", "LOADTEST_COUNT ?= ", "LOADTEST_ACCOUNT_NATS ?= ", "test -d cmd/loadtest", "go run ./cmd/loadtest -endpoint $(LOADTEST_ENDPOINT) -count $(LOADTEST_COUNT)", "-account-nats $(LOADTEST_ACCOUNT_NATS)"} {
		if !strings.Contains(body, want) {
			t.Errorf("Makefile lacks %q", want)
		}
	}
	if !strings.Contains(body, " loadtest ") && !strings.Contains(body, " loadtest\n") {
		t.Errorf("loadtest is not declared .PHONY")
	}
}
