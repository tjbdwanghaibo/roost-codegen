package roost

import (
	"strings"
	"testing"
)

func gameTemplateManifest(t *testing.T) Manifest {
	t.Helper()
	m := DefaultManifest("planet", "example.com/planet", []string{"game"}, []string{"configdata"}, nil)
	if err := applyGameTemplate(&m, "game"); err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	return m
}

// The game template hosts the four framework services as their own processes
// and wires the business service to all of them.
func TestGameTemplateHostsTheFourServicesAndWiresTheGame(t *testing.T) {
	m := gameTemplateManifest(t)
	for _, name := range []string{"account", "mail", "match", "chat"} {
		if m.Services[name].Framework != name {
			t.Errorf("service %s is not hosted: %+v", name, m.Services[name])
		}
	}
	if got := strings.Join(m.Services["game"].Uses, ","); got != "account,chat,mail,match" {
		t.Fatalf("game uses %q", got)
	}
	if !m.usesFrameworkServices() {
		t.Fatal("the template does not register as using roost-service")
	}
	if err := applyGameTemplate(&m, "nope"); err == nil {
		t.Fatal("a template applied to an undeclared service was accepted")
	}
}

// framework and uses are validated against the catalog and against each other.
func TestFrameworkServiceManifestValidation(t *testing.T) {
	base := func() Manifest {
		return DefaultManifest("planet", "example.com/planet", []string{"game"}, []string{"configdata"}, nil)
	}
	unknown := base()
	unknown.Services["ledger"] = ServiceSpec{Framework: "ledger"}
	if err := unknown.Validate(); err == nil || !strings.Contains(err.Error(), "unknown framework service") {
		t.Fatalf("unknown framework accepted: %v", err)
	}
	dangling := base()
	dangling.Services["game"] = ServiceSpec{Mods: []string{"configdata"}, Uses: []string{"mail"}}
	if err := dangling.Validate(); err == nil || !strings.Contains(err.Error(), "not a framework service") {
		t.Fatalf("uses of an undeclared framework service accepted: %v", err)
	}
	hostedUses := base()
	hostedUses.Services["mail"] = ServiceSpec{Framework: "mail", Uses: []string{"chat"}}
	hostedUses.Services["chat"] = ServiceSpec{Framework: "chat"}
	if err := hostedUses.Validate(); err == nil || !strings.Contains(err.Error(), "cannot also declare uses") {
		t.Fatalf("a hosted service with uses accepted: %v", err)
	}
	oldService := base()
	oldService.Versions.Service = "v1.4.0"
	if err := oldService.Validate(); err == nil || !strings.Contains(err.Error(), minimumVersions.Service) {
		t.Fatalf("a roost-service below the floor accepted: %v", err)
	}
	legacy := base()
	legacy.Versions.Service = "" // written by a generator that predates the field
	if err := legacy.Validate(); err != nil {
		t.Fatalf("an empty versions.service must read as latest: %v", err)
	}
}

// What the generator emits for a hosted service and for a service that calls
// it: the Server as the process, the owner Mod from the collaborators file,
// Redis and NATS assembled, the ClientMod and typed accessor on the caller,
// roost-service in go.mod and retained by frameworkdeps, and the service's
// configuration keys in its config file.
func TestGameTemplateRendersHostingAndClientWiring(t *testing.T) {
	m := gameTemplateManifest(t)
	plan, err := renderProject(m)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := string(plan["internal/bootstrap/generated.go"].Body)
	for _, want := range []string{
		`a.RegisterServer(app.ServiceName(svcmail.ServiceType), svcmail.NewServer()`,
		`svcmail.NewMod(serviceMail.Broadcast(), serviceMail.Metrics())`,
		`svcaccount.NewMod(serviceAccount.Verifier(), serviceAccount.Allocator(), serviceAccount.NameRules(), serviceAccount.Metrics())`,
		`svcchat.NewMod(serviceChat.Policy(), serviceChat.Bodies(), serviceChat.System(), serviceChat.Rules(), serviceChat.Metrics())`,
		`svcmatch.NewMod(serviceMatch.Grouping(), serviceMatch.Metrics())`,
		`kitredis.NewRedisMod()`, `kitnats.NewNatsMod(nil)`,
		`a.RegisterServer(app.ServiceName("game"), serviceGame.New()`,
		`svcaccount.NewClientMod()`, `svcmail.NewClientMod()`, `svcmatch.NewClientMod()`, `svcchat.NewClientMod()`,
	} {
		if !strings.Contains(bootstrap, want) {
			t.Errorf("bootstrap missing %q:\n%s", want, bootstrap)
		}
	}
	for _, path := range []string{
		"internal/service/mail/collaborators.go", "internal/service/account/collaborators.go",
		"internal/service/match/collaborators.go", "internal/service/chat/collaborators.go",
	} {
		file, ok := plan[path]
		if !ok || file.Owned {
			t.Errorf("%s must exist and be business-owned (owned=%v present=%v)", path, file.Owned, ok)
		}
	}
	if _, ok := plan["internal/service/mail/service.go"]; ok {
		t.Error("a hosted service must not get a business service.go")
	}
	clients, ok := plan["internal/service/game/framework_clients_gen.go"]
	if !ok || !clients.Owned {
		t.Fatalf("typed accessors missing or not generator-owned: present=%v", ok)
	}
	for _, want := range []string{"func Mail(r *app.Registry) (svcmail.Mail, error)", "func Account(r *app.Registry) (svcaccount.Accounts, error)", "svcchat.Messaging", "svcmatch.Matchmaker"} {
		if !strings.Contains(string(clients.Body), want) {
			t.Errorf("accessors missing %q", want)
		}
	}
	if gomod := string(plan["go.mod"].Body); !strings.Contains(gomod, "github.com/tjbdwanghaibo/roost-service "+minimumVersions.Service) {
		t.Errorf("go.mod does not require roost-service:\n%s", gomod)
	}
	if deps := string(plan["internal/frameworkdeps/generated.go"].Body); !strings.Contains(deps, "roost-service/servicemetrics") {
		t.Errorf("frameworkdeps does not retain roost-service:\n%s", deps)
	}
	mailConfig := string(plan["configs/service/config.mail.yaml"].Body)
	for _, want := range []string{"mail:\n  key_prefix: roost:planet:mail", "send_ttl: 720h", "redis:\n", "nats:\n"} {
		if !strings.Contains(mailConfig, want) {
			t.Errorf("mail config missing %q:\n%s", want, mailConfig)
		}
	}
	if accountConfig := string(plan["configs/service/config.account.prod.example.yaml"].Body); !strings.Contains(accountConfig, "session_secret: CHANGE_ME") {
		t.Errorf("account production example does not demand a secret:\n%s", accountConfig)
	}
	if gameConfig := string(plan["configs/service/config.game.yaml"].Body); !strings.Contains(gameConfig, "nats:\n") {
		t.Errorf("a service that calls framework services needs the bus, so nats config:\n%s", gameConfig)
	}
}

// A project that neither hosts nor calls a framework service carries no
// roost-service dependency at all.
func TestAProjectWithoutFrameworkServicesDoesNotDependOnRoostService(t *testing.T) {
	m := DefaultManifest("planet", "example.com/planet", []string{"game"}, []string{"configdata"}, nil)
	plan, err := renderProject(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"go.mod", "internal/frameworkdeps/generated.go", "internal/bootstrap/generated.go"} {
		if strings.Contains(string(plan[path].Body), "roost-service") {
			t.Errorf("%s mentions roost-service without any framework service", path)
		}
	}
}
