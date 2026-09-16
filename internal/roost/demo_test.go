package roost

import (
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
	for _, name := range []string{"account", "mail", "match", "chat"} {
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
	// The demo replaces the fail-closed skeleton; `roost config` refuses to
	// enable player TCP while the skeleton is still in place.
	if auth := read("internal/access/player/tcp/auth.go"); !strings.Contains(auth, demoTokenPrefixName) {
		t.Errorf("auth.go is not the demo authenticator:\n%s", auth)
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

// demoTokenPrefixName is the identifier the demo authenticator declares. The
// test asserts on the name rather than the literal so renaming the credential
// format does not silently pass.
const demoTokenPrefixName = "demoTokenPrefix"
