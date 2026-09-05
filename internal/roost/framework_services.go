package roost

import (
	"errors"
	"fmt"
	"go/format"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// frameworkServiceSpec describes one roost-service service a project can host
// as its own process (services.<name>.framework) and call from a business
// service (services.<name>.uses).
//
// Hosting means the generated bootstrap registers the service's Server as a
// subcommand with its owner Mod, on the Kit mods it depends on. Calling means
// the business service's process gets the ClientMod and a typed accessor.
// The collaborators the owner Mod requires — a Deliverer, a policy, an
// identity verifier — are business decisions, so they live in a file the
// generator creates once and never overwrites, with fail-closed defaults.
type frameworkServiceSpec struct {
	Package    string
	Interface  string
	Depends    []string
	ModArgs    []string
	Collabs    string
	ConfigFunc func(project string) string
}

const frameworkServiceModule = "github.com/tjbdwanghaibo/roost-service"

var frameworkCatalog = map[string]frameworkServiceSpec{
	"account": {
		Package: "account", Interface: "Accounts", Depends: []string{"redis", "nats"},
		ModArgs: []string{"Verifier()", "Allocator()", "NameRules()", "Metrics()"},
		Collabs: `// Verifier confirms a login identity with its channel (platform SDK,
// device attestation, …). The default refuses every login: an account
// service that cannot verify identities must not issue sessions.
func Verifier() account.IdentityVerifier {
	return account.VerifierFunc(func(context.Context, account.Identity) (account.Verified, error) {
		return account.Verified{}, errors.New("account: identity verifier is not configured; implement Verifier() in internal/service/%[1]s/collaborators.go")
	})
}

// Allocator mints player ids per game server. The default refuses: ids must be
// globally unique and come from a source this project chooses.
func Allocator() account.PlayerIDAllocator {
	return account.AllocatorFunc(func(context.Context, int32) (int64, error) {
		return 0, errors.New("account: player id allocator is not configured; implement Allocator() in internal/service/%[1]s/collaborators.go")
	})
}

// NameRules validates role names. The default bounds length and refuses
// whitespace; replace it with the project's own rules.
func NameRules() account.NameValidator {
	return account.NameValidatorFunc(func(raw string) error {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || len(trimmed) > 32 {
			return errors.New("name must be 1–32 characters")
		}
		if strings.ContainsAny(trimmed, " \t\n") {
			return errors.New("name must not contain whitespace")
		}
		return nil
	})
}
`,
		ConfigFunc: func(project string) string {
			return "account:\n  key_prefix: roost:" + project + ":account\n  session_secret: CHANGE_ME\n  session_ttl: 30m\n  claim_ttl: 5m\n"
		},
	},
	"mail": {
		Package: "mail", Interface: "Mail", Depends: []string{"redis", "nats"},
		ModArgs: []string{"Broadcast()", "Metrics()"},
		Collabs: `// Broadcast delivers broadcast mail to every online player. nil is a valid,
// fail-closed configuration: broadcasts are refused rather than dropped.
func Broadcast() mail.Deliverer { return nil }
`,
		ConfigFunc: func(project string) string {
			return "mail:\n  key_prefix: roost:" + project + ":mail\n  send_ttl: 720h\n  claim_lease: 30s\n"
		},
	},
	"match": {
		Package: "match", Interface: "Matchmaker", Depends: []string{"redis", "nats"},
		ModArgs: []string{"Grouping()", "Metrics()"},
		Collabs: `// Grouping decides which waiting tickets form a match. nil selects the
// default first-come grouping; replace it with the project's own rules.
func Grouping() match.Grouping { return nil }
`,
		ConfigFunc: func(project string) string {
			return "match:\n  key_prefix: roost:" + project + ":match\n  ticket_ttl: 60s\n  sweep_queues: []\n"
		},
	},
	"chat": {
		Package: "chat", Interface: "Messaging", Depends: []string{"redis", "nats"},
		ModArgs: []string{"Policy()", "Bodies()", "System()", "Rules()", "Metrics()"},
		Collabs: `// Policy decides who may publish to and read from a channel. The default
// refuses everything: a permissive policy is what a client-settable trust
// flag amounted to in the implementation roost-service replaces.
func Policy() chat.ChannelPolicy {
	return chat.PolicyFuncs{
		Publish: func(context.Context, chat.Sender, chat.Channel) error {
			return errors.New("chat: publish policy is not configured; implement Policy() in internal/service/%[1]s/collaborators.go")
		},
		Read: func(context.Context, chat.Sender, chat.Channel) error {
			return errors.New("chat: read policy is not configured; implement Policy() in internal/service/%[1]s/collaborators.go")
		},
	}
}

// Bodies registers the message body types this game allows.
func Bodies() *chat.BodyRegistry { return chat.NewBodyRegistry() }

// System authenticates the privileged system entry point from transport
// identity. The default refuses, so there is no system path until one is
// deliberately granted.
func System() chat.SystemAuthenticator {
	return chat.SystemAuthenticatorFunc(func(context.Context) (chat.SystemToken, error) {
		return chat.SystemToken{}, errors.New("chat: system authenticator is not configured")
	})
}

// Rules are the channel kinds and their scoping.
func Rules() []chat.ChannelRule { return chat.DefaultChannelRules() }
`,
		ConfigFunc: func(project string) string {
			return "chat:\n  key_prefix: roost:" + project + ":chat\n  retention_age: 168h\n  prune_channels: []\n"
		},
	},
}

func frameworkServiceNames() []string {
	names := make([]string, 0, len(frameworkCatalog))
	for name := range frameworkCatalog {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// isFrameworkService reports whether the named service hosts a roost-service
// service instead of business code.
func (m Manifest) isFrameworkService(name string) bool {
	return strings.TrimSpace(m.Services[name].Framework) != ""
}

// usesFrameworkServices reports whether anything in the project depends on
// roost-service: a hosted service or a business service that calls one.
func (m Manifest) usesFrameworkServices() bool {
	for _, service := range m.Services {
		if strings.TrimSpace(service.Framework) != "" || len(service.Uses) > 0 {
			return true
		}
	}
	return false
}

// effectiveServiceMods is a service's Kit mod set as the generator assembles
// it: the declared mods, plus what a hosted framework service depends on
// (Redis for state, NATS for the bus), plus NATS for a business service that
// calls a framework service through its ClientMod.
func effectiveServiceMods(m Manifest, name string) []string {
	service := m.Services[name]
	mods := append([]string(nil), service.Mods...)
	if spec, ok := frameworkCatalog[strings.TrimSpace(service.Framework)]; ok {
		mods = append(mods, spec.Depends...)
	}
	if len(service.Uses) > 0 {
		mods = append(mods, "nats")
	}
	return mods
}

func (m Manifest) validateFrameworkServices() error {
	var joined error
	names := sortedServiceNames(m)
	for _, name := range names {
		service := m.Services[name]
		framework := strings.TrimSpace(service.Framework)
		if framework != "" {
			if _, ok := frameworkCatalog[framework]; !ok {
				joined = errors.Join(joined, fmt.Errorf("service %s: unknown framework service %q; known: %s", name, framework, strings.Join(frameworkServiceNames(), ", ")))
			}
			if len(service.Uses) > 0 {
				joined = errors.Join(joined, fmt.Errorf("service %s hosts %s and cannot also declare uses; call other services from a business service", name, framework))
			}
			if access, ok := m.Access["player"]; ok && access.Service == name {
				joined = errors.Join(joined, fmt.Errorf("access.player cannot target framework service %s", name))
			}
		}
		seen := map[string]bool{}
		for _, used := range service.Uses {
			if seen[used] {
				joined = errors.Join(joined, fmt.Errorf("service %s repeats uses %q", name, used))
			}
			seen[used] = true
			target, ok := m.Services[used]
			if !ok || strings.TrimSpace(target.Framework) == "" {
				joined = errors.Join(joined, fmt.Errorf("service %s uses %q, which is not a framework service declared in services", name, used))
			}
		}
	}
	return joined
}

// renderFrameworkCollaborators is the business-owned file a hosted framework
// service's owner Mod is built from. Created once, never overwritten.
func renderFrameworkCollaborators(m Manifest, name string) string {
	spec := frameworkCatalog[m.Services[name].Framework]
	body := strings.ReplaceAll(spec.Collabs, "%[1]s", name)
	needsErrors := strings.Contains(body, "errors.New")
	needsStrings := strings.Contains(body, "strings.")
	needsContext := strings.Contains(body, "context.Context")
	var imports []string
	if needsContext {
		imports = append(imports, `"context"`)
	}
	if needsErrors {
		imports = append(imports, `"errors"`)
	}
	if needsStrings {
		imports = append(imports, `"strings"`)
	}
	imports = append(imports, "", `"github.com/tjbdwanghaibo/roost-service/servicemetrics"`, fmt.Sprintf("%q", frameworkServiceModule+"/"+spec.Package))
	var b strings.Builder
	fmt.Fprintf(&b, "// Package %s supplies the collaborators the %s service needs from this\n// project. roost-codegen created this file once and will not overwrite it.\n", safeIdent(name), spec.Package)
	fmt.Fprintf(&b, "package %s\n\nimport (\n", safeIdent(name))
	for _, imp := range imports {
		if imp == "" {
			b.WriteString("\n")
			continue
		}
		fmt.Fprintf(&b, "\t%s\n", imp)
	}
	b.WriteString(")\n\n")
	b.WriteString(body)
	b.WriteString("\n// Metrics receives the service's counters. nil means no reporting and never\n// fails an operation; wire the project's servicemetrics.Reporter here.\nfunc Metrics() servicemetrics.Reporter { return nil }\n")
	return b.String()
}

// renderFrameworkClients is the generated accessor file for a business
// service that calls framework services: one typed lookup per `uses` entry.
func renderFrameworkClients(m Manifest, name string) string {
	service := m.Services[name]
	used := append([]string(nil), service.Uses...)
	sort.Strings(used)
	var b strings.Builder
	b.WriteString(generatedHeader + "\n")
	fmt.Fprintf(&b, "package %s\n\nimport (\n\t\"fmt\"\n\n\t\"github.com/tjbdwanghaibo/roost-core/app\"\n", safeIdent(name))
	for _, target := range used {
		spec := frameworkCatalog[m.Services[target].Framework]
		fmt.Fprintf(&b, "\tsvc%s %q\n", spec.Package, frameworkServiceModule+"/"+spec.Package)
	}
	b.WriteString(")\n")
	for _, target := range used {
		spec := frameworkCatalog[m.Services[target].Framework]
		fmt.Fprintf(&b, `
// %[1]s returns the %[2]s service this process reaches through %[2]s.NewClientMod
// (roost.yaml: services.%[3]s.uses). Call it from Init and keep the result.
func %[1]s(r *app.Registry) (svc%[2]s.%[4]s, error) {
	service, ok := app.Lookup[svc%[2]s.%[4]s](r, svc%[2]s.CapabilityName)
	if !ok || service == nil {
		return nil, fmt.Errorf("%[3]s: capability %%q not found; is the %[2]s process running and reachable over the bus?", svc%[2]s.CapabilityName)
	}
	return service, nil
}
`, safeIdent(target), spec.Package, name, spec.Interface)
	}
	return b.String()
}

// applyGameTemplate turns a fresh manifest into the game template: the
// business service calls account, mail, match and chat, each hosted as its own
// process. It is opt-in (`roost project new … -template game`).
func applyGameTemplate(m *Manifest, gameService string) error {
	service, ok := m.Services[gameService]
	if !ok {
		return fmt.Errorf("template game: service %q is not declared", gameService)
	}
	for _, name := range frameworkServiceNames() {
		if existing, taken := m.Services[name]; taken && strings.TrimSpace(existing.Framework) == "" {
			return fmt.Errorf("template game: service name %q is taken by a business service", name)
		}
		m.Services[name] = ServiceSpec{Framework: name}
		if !contains(service.Uses, name) {
			service.Uses = append(service.Uses, name)
		}
	}
	sort.Strings(service.Uses)
	// Player and World are Nest entities, so the game process runs the Nest
	// runtime (which brings the dataengine, Mongo and NATS).
	if resolved, err := resolveMods(service.Mods); err == nil && !contains(resolved, "nest") {
		service.Mods = append(service.Mods, "nest")
	}
	m.Services[gameService] = service
	return nil
}

// scaffoldGameTemplate adds what the game template owns beyond the manifest:
// the Player and World entities with their lifecycles, the World singleton
// accessor, and a game Service that ensures the World exists before serving.
// Everything it writes is business-owned and written once.
func scaffoldGameTemplate(root string, m Manifest, gameService string) ([]string, error) {
	var created []string
	for _, step := range []AddOptions{
		{Kind: "entity", Name: "Player"},
		{Kind: "entity", Name: "World"},
		{Kind: "lifecycle", Name: "Player", Service: gameService},
		{Kind: "lifecycle", Name: "World", Service: gameService},
	} {
		files, err := Add(root, step)
		if err != nil {
			return created, fmt.Errorf("template game: add %s %s: %w", step.Kind, step.Name, err)
		}
		created = append(created, files...)
	}
	for rel, body := range map[string]string{
		"game/lifecycle/world_singleton.go":               renderWorldSingleton(m),
		"internal/service/" + gameService + "/service.go": renderGameTemplateService(m, gameService),
	} {
		formatted, err := format.Source([]byte(body))
		if err != nil {
			return created, fmt.Errorf("template game: format %s: %w\n%s", rel, err, body)
		}
		if err := writeAtomic(filepath.Join(root, filepath.FromSlash(rel)), formatted, 0o644); err != nil {
			return created, err
		}
		created = append(created, rel)
	}
	if err := Generate(root, GenerateOptions{Stdout: io.Discard}); err != nil {
		return created, fmt.Errorf("template game: regenerate: %w", err)
	}
	return created, nil
}

// renderWorldSingleton is the one-World-per-process accessor. The World is an
// ordinary Nest entity with a fixed unique id, created on the first start of
// this process and loaded on every later one; "singleton" is a property of
// this process, not of the cluster.
func renderWorldSingleton(m Manifest) string {
	return fmt.Sprintf(`package lifecycle

import (
	"context"
	"fmt"

	world %q
	"github.com/tjbdwanghaibo/roost-core/app"
)

// WorldUniqueID is the unique id of this process's one World. There is
// exactly one World per game process: every game server has its own.
const WorldUniqueID int64 = 1

// EnsureWorld returns this process's World, creating it on the first start
// and loading it on every later one. The game Service calls it from Init, so
// the World exists before any request is served.
func EnsureWorld(ctx context.Context, registry *app.Registry) (*world.World, error) {
	lifecycle, err := WorldFromRegistry(registry)
	if err != nil {
		return nil, err
	}
	value, _, err := lifecycle.GetOrCreate(ctx, WorldUniqueID)
	if err != nil {
		return nil, fmt.Errorf("world: ensure singleton: %%w", err)
	}
	return value, nil
}
`, m.Project.Module+"/game/entities/world")
}

// renderGameTemplateService is the template's business Service: the plain
// scaffold plus the World it owns.
func renderGameTemplateService(m Manifest, name string) string {
	return fmt.Sprintf(`package %s

import (
	"context"
	"fmt"

	world %q
	lifecycle %q
	"github.com/tjbdwanghaibo/roost-core/app"
)

// Service is the %s server. It owns this process's World.
type Service struct {
	world *world.World
}

func New() *Service                    { return &Service{} }
func (*Service) Name() app.ServiceName { return app.ServiceName(%q) }

// Init runs after every Mod has started, so the Entity runtime is up. The
// World — this process's one singleton Entity — is created or loaded here,
// before any request is served; a game server without its World does not
// start.
func (s *Service) Init(registry *app.Registry) error {
	value, err := lifecycle.EnsureWorld(context.Background(), registry)
	if err != nil {
		return fmt.Errorf("%s: %%w", err)
	}
	s.world = value
	return nil
}

// World is this process's World. It is set in Init and never nil afterwards.
func (s *Service) World() *world.World { return s.world }

func (*Service) Serve(ctx context.Context) error { <-ctx.Done(); return nil }
func (*Service) Shutdown(context.Context) error  { return nil }

var _ app.Service = (*Service)(nil)
`, safeIdent(name), m.Project.Module+"/game/entities/world", m.Project.Module+"/game/lifecycle", name, name, name)
}
