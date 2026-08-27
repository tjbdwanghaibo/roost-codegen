package roost

import (
	"bytes"
	"fmt"
	"go/format"
	"sort"
	"strings"
)

func renderProject(m Manifest) (map[string]plannedFile, error) {
	files := map[string]plannedFile{}
	add := func(path, body string, owned bool) { files[path] = plannedFile{Body: []byte(body), Owned: owned} }
	addGo := func(path, body string, owned bool) error {
		formatted, err := format.Source([]byte(body))
		if err != nil {
			return fmt.Errorf("format %s: %w\n%s", path, err, body)
		}
		files[path] = plannedFile{Body: formatted, Owned: owned}
		return nil
	}

	add("go.mod", renderGoMod(m), false)
	add(".gitignore", "bin/\ndist/\nlog/\n.env\n*.local.yaml\n.idea/\n.vscode/\n", false)
	add("Makefile", renderMakefile(m), true)
	add("README.md", renderProjectReadme(m), false)
	add("docs/USAGE.zh-CN.md", renderUsage(m), true)
	add(".github/workflows/ci.yml", renderCI(), true)
	add("deploy/dev/docker-compose.yaml", renderCompose(m), true)
	add("Dockerfile", renderDockerfile(m), false)
	if err := addGo("main.go", renderMain(m), false); err != nil {
		return nil, err
	}
	if err := addGo("internal/bootstrap/generated.go", renderBootstrap(m), true); err != nil {
		return nil, err
	}

	services := sortedServiceNames(m)
	for _, name := range services {
		if err := addGo("internal/service/"+name+"/service.go", renderService(name), false); err != nil {
			return nil, err
		}
		add("configs/service/config."+name+".yaml", renderServiceConfig(m, name, false), false)
		add("configs/service/config."+name+".prod.example.yaml", renderServiceConfig(m, name, true), false)
	}
	if hasFeature(m, "config") {
		if err := addGo("configs/schema/doc.go", "package schema\n", false); err != nil {
			return nil, err
		}
		if err := addGo("configs/generated/doc.go", "package generated\n", false); err != nil {
			return nil, err
		}
		add("configs/data/_manifest.json", "{\n  \"version\": 1,\n  \"tables\": {}\n}\n", false)
	}
	featurePackages := map[string]string{
		"protocol": "protocol/def/doc.go", "entity": "game/entities/doc.go", "nest": "game/handler/doc.go",
		"event": "event/def/doc.go", "dao": "db/def/doc.go", "attribute": "game/gameplay/attribute/doc.go",
		"webroute": "service/web/doc.go",
	}
	for feature, path := range featurePackages {
		if !hasFeature(m, feature) {
			continue
		}
		pkg := strings.TrimSuffix(path[strings.LastIndex(path, "/")+1:], ".go")
		_ = pkg
		dir := path[:strings.LastIndex(path, "/")]
		packageName := dir[strings.LastIndex(dir, "/")+1:]
		if packageName == "def" {
			packageName = feature + "def"
		}
		if feature == "dao" {
			packageName = "dbdef"
		}
		body := "package " + packageName + "\n"
		if feature == "webroute" {
			body += "\ntype Service struct{}\n"
		}
		if err := addGo(path, body, false); err != nil {
			return nil, err
		}
	}
	if hasFeature(m, "dao") {
		if err := addGo("db/doc.go", "package db\n", false); err != nil {
			return nil, err
		}
	}
	if hasFeature(m, "event") {
		if err := addGo("event/doc.go", "package event\n", false); err != nil {
			return nil, err
		}
	}
	if hasFeature(m, "nest") {
		if err := addGo("game/bootstrap/register.go", "package bootstrap\n\nimport \"sync\"\n\nvar nestOnce sync.Once\n", false); err != nil {
			return nil, err
		}
	}
	if hasFeature(m, "replication-quic") || hasFeature(m, "replication-kcp") || hasFeature(m, "replication-udp") {
		if err := addGo("internal/transport/generated.go", renderReplication(m), true); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func renderReplication(m Manifest) string {
	var b strings.Builder
	b.WriteString(generatedHeader + "\npackage transport\n\nimport (\n")
	b.WriteString("\t\"context\"\n")
	if hasFeature(m, "replication-udp") {
		b.WriteString("\t\"net\"\n")
	}
	b.WriteString("\tcoreentitysync \"github.com/tjbdwanghaibo/cube-core/entitysync\"\n")
	b.WriteString("\tcorerep \"github.com/tjbdwanghaibo/cube-core/replication\"\n")
	b.WriteString("\tkitrep \"github.com/tjbdwanghaibo/cube-kit/replication\"\n")
	b.WriteString("\tkitsync \"github.com/tjbdwanghaibo/cube-kit/sync\"\n)\n\n")
	b.WriteString("func AsyncConfig() kitrep.AsyncTransportConfig { return kitrep.DefaultAsyncTransportConfig() }\n\n")
	b.WriteString("type SessionResolver func(coreentitysync.SubscriberRef) (corerep.SessionID, error)\n\n")
	b.WriteString("func NewRoomSink(async *kitrep.AsyncTransport, resolve SessionResolver) (*kitsync.RoomTransportSink, error) {\n")
	b.WriteString("\tif resolve == nil { return nil, kitsync.ErrRoomSessionResolver }\n")
	b.WriteString("\treturn kitsync.NewRoomTransportSink(kitsync.RoomTransportSinkConfig{Transport: async, Sessions: kitsync.RoomSessionResolverFunc(func(_ context.Context, subscriber coreentitysync.SubscriberRef) (corerep.SessionID, error) { return resolve(subscriber) })})\n}\n\n")
	if hasFeature(m, "replication-quic") {
		b.WriteString("func NewQUIC() (*kitrep.AsyncTransport, *kitrep.QUICTransport, error) {\n\tprotocol := kitrep.NewQUICTransport(kitrep.QUICTransportConfig{})\n\tasync, err := kitrep.NewAsyncTransport(protocol, AsyncConfig())\n\treturn async, protocol, err\n}\n\n")
	}
	if hasFeature(m, "replication-kcp") {
		b.WriteString("func NewKCP() (*kitrep.AsyncTransport, *kitrep.KCPTransport, error) {\n\tprotocol, err := kitrep.NewKCPTransport(kitrep.DefaultKCPTransportConfig())\n\tif err != nil { return nil, nil, err }\n\tasync, err := kitrep.NewAsyncTransport(protocol, AsyncConfig())\n\treturn async, protocol, err\n}\n\n")
	}
	if hasFeature(m, "replication-udp") {
		b.WriteString("func NewUDP(conn net.PacketConn, reliable kitrep.ReliableSender) (*kitrep.AsyncTransport, *kitrep.UDPTransport, error) {\n\tprotocol, err := kitrep.NewUDPTransport(kitrep.UDPTransportConfig{PacketConn: conn})\n\tif err != nil { return nil, nil, err }\n\tcomposite := kitrep.CompositeTransport{Datagrams: protocol, Reliable: reliable}\n\tasync, err := kitrep.NewAsyncTransport(composite, AsyncConfig())\n\treturn async, protocol, err\n}\n")
	}
	return b.String()
}

func renderGoMod(m Manifest) string {
	return fmt.Sprintf(`module %s

go 1.25.0

require (
	github.com/tjbdwanghaibo/cube-core v0.0.0
	github.com/tjbdwanghaibo/cube-kit v0.0.0
	github.com/tjbdwanghaibo/cube-skill v0.0.0
)

replace github.com/tjbdwanghaibo/cube-core => github.com/tjbdwanghaibo/roost-core %s
replace github.com/tjbdwanghaibo/cube-kit => github.com/tjbdwanghaibo/roost-kit %s
replace github.com/tjbdwanghaibo/cube-skill => github.com/tjbdwanghaibo/roost-skill %s
`, m.Project.Module, m.Versions.Core, m.Versions.Kit, m.Versions.Skill)
}

func renderMain(m Manifest) string {
	return fmt.Sprintf(`package main

import (
	"log/slog"
	"os"

	"%s/internal/bootstrap"
)

func main() {
	a, err := bootstrap.New()
	if err == nil { err = a.Execute() }
	if err != nil {
		slog.Error("server exit", "err", err)
		os.Exit(1)
	}
}
`, m.Project.Module)
}

func renderBootstrap(m Manifest) string {
	imports := map[string]string{
		"github.com/tjbdwanghaibo/cube-core/app":           "",
		"github.com/tjbdwanghaibo/cube-core/app/buildinfo": "",
	}
	allMods := allProjectMods(m)
	// checkpoint constructors require the instance-scoped EntityAccess even
	// when a project enables them transitively (for example through Saga)
	// without generating Nest handlers.
	instanceRuntime := contains(allMods, "checkpoint") || contains(allMods, "nest") || contains(allMods, "remote_entity")
	if instanceRuntime {
		imports["github.com/tjbdwanghaibo/cube-core/entity"] = ""
	}
	for _, name := range allMods {
		spec := modCatalog[name]
		imports[spec.ImportPath] = spec.Alias
	}
	services := sortedServiceNames(m)
	for _, name := range services {
		imports[m.Project.Module+"/internal/service/"+name] = "service" + safeIdent(name)
	}
	if hasFeature(m, "config") && contains(allMods, "configdata") {
		imports["github.com/tjbdwanghaibo/cube-core/configdata"] = "coreconfigdata"
		imports[m.Project.Module+"/configs/generated"] = "generatedconfig"
	}
	if hasFeature(m, "nest") {
		imports[m.Project.Module+"/game/bootstrap"] = "gamebootstrap"
	}
	for _, name := range m.Sagas {
		imports[m.Project.Module+"/saga/"+name] = "saga" + safeIdent(name)
	}
	var b strings.Builder
	b.WriteString(generatedHeader + "\npackage bootstrap\n\nimport (\n")
	paths := make([]string, 0, len(imports))
	for path := range imports {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if alias := imports[path]; alias != "" {
			fmt.Fprintf(&b, "\t%s %q\n", alias, path)
		} else {
			fmt.Fprintf(&b, "\t%q\n", path)
		}
	}
	b.WriteString(")\n")
	if instanceRuntime {
		b.WriteString("\nvar EntityManager = entity.NewEntityManager()\n")
		b.WriteString("var EntityAccess = entity.NewManagerAccess(EntityManager)\n")
	}
	b.WriteString("\nfunc New() (*app.App, error) {\n")
	if hasFeature(m, "nest") {
		b.WriteString("\tgamebootstrap.RegisterNestHandlers()\n")
	}
	if hasFeature(m, "config") && contains(allMods, "configdata") {
		b.WriteString("\tif err := generatedconfig.RegisterGeneratedConfigData(coreconfigdata.DefaultRegistry()); err != nil { return nil, err }\n")
	}
	fmt.Fprintf(&b, "\ta := app.New(%q, buildinfo.VersionString())\n", m.Project.Name)
	shared, _ := resolveMods(m.SharedMods)
	if len(shared) > 0 {
		b.WriteString("\ta.Mods(\n")
		for _, name := range shared {
			fmt.Fprintf(&b, "\t\t%s,\n", renderModConstructor(m, name, allMods))
		}
		b.WriteString("\t)\n")
	}
	for _, name := range services {
		mods, _ := resolveMods(m.Services[name].Mods)
		fmt.Fprintf(&b, "\ta.RegisterServer(app.ServiceName(%q), service%s.New()", name, safeIdent(name))
		for _, mod := range mods {
			fmt.Fprintf(&b, ",\n\t\t%s", renderModConstructor(m, mod, allMods))
		}
		b.WriteString(")\n")
	}
	b.WriteString("\treturn a, nil\n}\n")
	return b.String()
}

func renderModConstructor(m Manifest, name string, allMods []string) string {
	switch name {
	case "checkpoint":
		return "kitcheckpoint.NewMod(kitcheckpoint.WithEntityAccess(EntityAccess))"
	case "remote_entity":
		return "kitremoteentity.NewRemoteEntityMod(0, kitremoteentity.WithMongoStorage(EntityAccess))"
	case "nestwal":
		return fmt.Sprintf("kitnestwal.NewMod(%t)", contains(allMods, "remote_entity"))
	case "nest":
		return "kitnest.NewMod(EntityAccess)"
	case "saga":
		if len(m.Sagas) == 0 {
			return "kitsaga.NewMod()"
		}
		definitions := make([]string, 0, len(m.Sagas))
		for _, sagaName := range m.Sagas {
			definitions = append(definitions, "saga"+safeIdent(sagaName)+".Definitions()")
		}
		return "kitsaga.NewMod(kitsaga.CombineDefinitions(" + strings.Join(definitions, ", ") + ")...)"
	default:
		return modCatalog[name].Constructor
	}
}

func renderService(name string) string {
	return fmt.Sprintf(`package %s

import (
	"context"

	"github.com/tjbdwanghaibo/cube-core/app"
)

type Service struct{}

func New() *Service { return &Service{} }
func (*Service) Name() app.ServiceName { return app.ServiceName(%q) }
func (*Service) Init(*app.Registry) error { return nil }
func (*Service) Serve(ctx context.Context) error { <-ctx.Done(); return nil }
func (*Service) Shutdown(context.Context) error { return nil }

var _ app.Service = (*Service)(nil)
`, safeIdent(name), name)
}

func renderServiceConfig(m Manifest, service string, production bool) string {
	mods, _ := resolveMods(append(append([]string{}, m.SharedMods...), m.Services[service].Mods...))
	var b strings.Builder
	b.WriteString("# Generated starter configuration; application-owned after project creation.\n")
	b.WriteString("sid: 1000\n")
	b.WriteString("log:\n  level: info\n  json: true\n  stdout: true\n  file: true\n  dir: log\n")
	b.WriteString("shutdown:\n  total_timeout: 30s\n  serve_wait_timeout: 5s\n")
	seen := map[string]bool{}
	for _, name := range mods {
		if !seen[name] && modCatalog[name].Config != "" {
			b.WriteString(modCatalog[name].Config)
			seen[name] = true
		}
	}
	if production {
		value := strings.ReplaceAll(strings.ReplaceAll(b.String(), "127.0.0.1", "CHANGE_ME"), "localhost", "CHANGE_ME")
		value = strings.ReplaceAll(value, "replicas: 1", "replicas: 3")
		value = strings.ReplaceAll(value, "aof_replicas: 0", "aof_replicas: 1")
		value = strings.ReplaceAll(value, "file: true", "file: false")
		return strings.ReplaceAll(value, "dir: data/wal/nest", "dir: /var/lib/roost/wal")
	}
	return b.String()
}

func sortedServiceNames(m Manifest) []string {
	names := make([]string, 0, len(m.Services))
	for name := range m.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func safeIdent(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '_' || r == '-' })
	for i := range parts {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func hasFeature(m Manifest, feature string) bool { return contains(m.Features, feature) }
func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func renderMakefile(m Manifest) string {
	service := sortedServiceNames(m)[0]
	return fmt.Sprintf(`# Code generated by roost-codegen. DO NOT EDIT.
APP_NAME := %s
CODEGEN_MODULE := github.com/tjbdwanghaibo/roost-codegen
CODEGEN_VERSION ?= %s
ROOST := go run $(CODEGEN_MODULE)/cmd/roost@$(CODEGEN_VERSION)
SERVICE ?= %s
SID ?= 1000
CONFIG ?= configs/service/config.$(SERVICE).yaml
VERSION ?= dev
COMMIT := $(shell git rev-parse --short HEAD)
BUILD_TIME := $(shell git show -s --format=%%cI HEAD)
DIRTY := $(shell git status --porcelain)
LDFLAGS := -X github.com/tjbdwanghaibo/cube-core/app/buildinfo.Version=$(VERSION) -X github.com/tjbdwanghaibo/cube-core/app/buildinfo.Commit=$(COMMIT) -X github.com/tjbdwanghaibo/cube-core/app/buildinfo.BuildTime=$(BUILD_TIME) -X github.com/tjbdwanghaibo/cube-core/app/buildinfo.Dirty=$(DIRTY)

.PHONY: help sync doctor fmt fmt-check vet test test-race build run generate generate-changed check-generated config-check id-check ci dev-up dev-down dev-logs clean
.PHONY: new-service add-mod new-module new-protocol new-entity new-component new-event new-table new-dao new-webroute new-errcode new-saga

help:
	$(ROOST) help-make
sync:
	$(ROOST) project sync
doctor:
	$(ROOST) project doctor
fmt:
	go fmt ./...
fmt-check:
	$(ROOST) format check
vet:
	go vet ./...
test:
	go test ./...
test-race:
	go test -race ./...
build:
	go build -ldflags "$(LDFLAGS)" ./...
run:
	go run . $(SERVICE) --sid $(SID) --config $(CONFIG)
generate:
	$(ROOST) generate
generate-changed:
	$(ROOST) generate --changed
check-generated:
	$(ROOST) generate --check
config-check:
	$(ROOST) config check --service $(SERVICE)
id-check:
	$(ROOST) id check
new-service:
	$(ROOST) add service $(NAME) -mods $(MODS)
add-mod:
	$(ROOST) add mod $(NAME) -service $(SERVICE)
new-module:
	$(ROOST) add module $(NAME)
new-protocol:
	$(ROOST) add protocol $(NAME) -group $(GROUP)
new-entity:
	$(ROOST) add entity $(NAME)
new-component:
	$(ROOST) add component $(NAME)
new-event:
	$(ROOST) add event $(NAME)
new-table:
	$(ROOST) add table $(NAME)
new-dao:
	$(ROOST) add dao $(NAME)
new-webroute:
	$(ROOST) add webroute $(NAME)
new-errcode:
	$(ROOST) add errcode $(NAME)
new-saga:
	$(ROOST) add saga $(NAME) -service $(SERVICE) -steps $(STEPS)
ci: fmt-check vet test test-race check-generated config-check id-check
dev-up:
	docker compose -f deploy/dev/docker-compose.yaml up -d
dev-down:
	docker compose -f deploy/dev/docker-compose.yaml down
dev-logs:
	docker compose -f deploy/dev/docker-compose.yaml logs -f
clean:
	go clean
`, m.Project.Name, codegenVersion(m), service)
}

// codegenVersion resolves the Makefile's CODEGEN_VERSION: an empty
// versions.codegen means "track latest" — projects pin only when they set an
// explicit version in roost.yaml (or override per-invocation via
// `make generate CODEGEN_VERSION=vX.Y.Z`).
func codegenVersion(m Manifest) string {
	if v := strings.TrimSpace(m.Versions.Codegen); v != "" {
		return v
	}
	return "latest"
}

func renderCI() string {
	return `# Code generated by roost-codegen. DO NOT EDIT.
name: ci
on:
  push:
  pull_request:
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true
      - run: go mod download
      - run: make ci
`
}

func renderCompose(m Manifest) string {
	needed := map[string]bool{}
	for _, name := range allProjectMods(m) {
		if svc := modCatalog[name].DevService; svc != "" {
			needed[svc] = true
		}
	}
	var b strings.Builder
	b.WriteString("# Code generated by roost-codegen. DO NOT EDIT.\nservices:\n")
	if needed["redis"] {
		b.WriteString("  redis:\n    image: redis:7.4-alpine\n    command: [\"redis-server\", \"--appendonly\", \"yes\", \"--appendfsync\", \"everysec\"]\n    ports: [\"6379:6379\"]\n    volumes: [\"redis-data:/data\"]\n")
	}
	if needed["mongo"] {
		b.WriteString("  mongo:\n    image: mongo:8.0\n    command: [\"mongod\", \"--replSet\", \"rs0\", \"--bind_ip_all\"]\n    ports: [\"27017:27017\"]\n    volumes: [\"mongo-data:/data/db\"]\n    healthcheck:\n      test: [\"CMD\", \"mongosh\", \"--quiet\", \"--eval\", \"db.adminCommand('ping').ok\"]\n      interval: 2s\n      timeout: 2s\n      retries: 30\n  mongo-init:\n    image: mongo:8.0\n    restart: \"no\"\n    depends_on:\n      mongo:\n        condition: service_healthy\n    entrypoint: [\"mongosh\", \"--host\", \"mongo:27017\", \"--quiet\", \"--eval\", \"try { rs.status() } catch (e) { rs.initiate({_id:'rs0',members:[{_id:0,host:'mongo:27017'}]}) }\"]\n")
	}
	if needed["nats"] {
		b.WriteString("  nats:\n    image: nats:2.11-alpine\n    command: [\"-js\", \"-sd\", \"/data\", \"-m\", \"8222\"]\n    ports: [\"4222:4222\", \"8222:8222\"]\n    volumes: [\"nats-data:/data\"]\n")
	}
	if needed["etcd"] {
		b.WriteString("  etcd:\n    image: quay.io/coreos/etcd:v3.5.21\n    command: [\"etcd\", \"--advertise-client-urls=http://0.0.0.0:2379\", \"--listen-client-urls=http://0.0.0.0:2379\"]\n    ports: [\"2379:2379\"]\n    volumes: [\"etcd-data:/etcd-data\"]\n")
	}
	b.WriteString("volumes:\n")
	for _, name := range []string{"redis", "mongo", "nats", "etcd"} {
		if needed[name] {
			fmt.Fprintf(&b, "  %s-data: {}\n", name)
		}
	}
	return b.String()
}

func renderDockerfile(m Manifest) string {
	wal := contains(allProjectMods(m), "nestwal")
	walBuild, walRuntime := "", ""
	if wal {
		walBuild = "RUN mkdir -p /out/wal\n"
		walRuntime = "COPY --chown=65532:65532 --from=build /out/wal /var/lib/roost/wal\nVOLUME [\"/var/lib/roost/wal\"]\n"
	}
	return fmt.Sprintf(`FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/%s .
%s

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --chown=65532:65532 --from=build /out/%s /app/%s
COPY configs /app/configs
%sUSER nonroot:nonroot
ENTRYPOINT ["/app/%s"]
`, m.Project.Name, walBuild, m.Project.Name, m.Project.Name, walRuntime, m.Project.Name)
}

func renderProjectReadme(m Manifest) string {
	return fmt.Sprintf(`# %s

Generated with roost-codegen. Project configuration lives in roost.yaml.

    make dev-up
    make generate
    make run SERVICE=%s
    make ci

See [docs/USAGE.zh-CN.md](docs/USAGE.zh-CN.md) for complete usage.
`, m.Project.Name, sortedServiceNames(m)[0])
}

func renderUsage(m Manifest) string {
	var services bytes.Buffer
	for _, name := range sortedServiceNames(m) {
		fmt.Fprintf(&services, "- %s: mods %s\n", name, strings.Join(m.Services[name].Mods, ","))
	}
	return fmt.Sprintf(`<!-- Code generated by roost-codegen. DO NOT EDIT. -->
# %s 使用说明

本文件由 roost-codegen 根据 roost.yaml 生成。

## 常用流程

1. 修改 roost.yaml 声明 Service、Kit Mod 和 feature。
2. 执行 make sync 更新受控装配文件。
3. 执行 make generate-changed 更新受影响的代码。
4. 提交前执行 make ci。

## 服务

%s
## 命令

- make run SERVICE=<name> SID=<id>：启动服务。
- make dev-up/dev-down：启动或停止所需基础设施。
- make doctor：检查清单、依赖、配置和生成状态。
- make config-check SERVICE=<name>：检查服务配置。
- make id-check：检查协议、实体、组件和错误码 ID。
- make check-generated：在临时目录验证生成结果，不污染工作区。

## 安全约定

生产配置使用 config.<service>.prod.example.yaml 为模板。必须替换 CHANGE_ME，配置真实认证、TLS、超时和 advertise address；不要提交真实密钥。

- MongoDB 必须使用副本集；启动会拒绝 standalone，并固定 majority write concern 与 snapshot transaction read concern。
- Checkpoint WAL 要求 Redis 7.2+、AOF 已开启和单主/Sentinel 接入；生产默认还要求至少 1 个副本完成 AOF fsync。Redis Cluster 会因无法保证 Lua 与 WAITAOF 同连接同分片而拒绝启动。
- /var/lib/roost/wal 必须挂载单写持久卷，同一个 SID 不得由两个实例同时挂载。
- JetStream effect 消费必须通过 nestwal.MongoEffectInbox 与业务 Mongo 写入同一事务，不能仅依赖 MsgID 去重窗口。
- 发布顺序为 roost-core、roost-kit、roost-codegen；部署前必须执行 make ci 并完成容器重启恢复演练。
`, m.Project.Name, services.String())
}
