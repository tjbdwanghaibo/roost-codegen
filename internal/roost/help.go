package roost

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

type helpTopic struct {
	Name          string
	Aliases       []string
	Summary       string
	Usage         string
	Configuration string
	Example       string
}

var helpTopics = []helpTopic{
	{
		Name: "project", Aliases: []string{"project-new", "project-sync", "project-diff", "project-doctor", "project-upgrade", "upgrade-project"},
		Summary: "创建、同步、检查和升级完整 Roost 工程",
		Usage: `roost project new <name> [-module path] [-out dir] [-services a,b] [-mods a,b] [-features a,b]
roost project sync|diff|doctor [--root dir]
roost project upgrade [--root dir] [--dry-run] [-core version] [-kit version] [-skill version] [-codegen version]
make project-upgrade`,
		Configuration: `项目声明位于 roost.yaml：project 定义名称/module，versions 定义 latest 或最低版本，shared_mods/services 定义装配，features 定义生成能力，ids 定义编号空间。sync/upgrade 只覆盖带生成标识的 codegen 受控文件；project-upgrade 始终通过 @latest 运行，并将 core、kit、skill、codegen 策略全部更新为 latest。升级读取旧清单时先合并新策略，再执行当前版本门禁。`,
		Example: `roost project new planet -module example.com/planet -services game,gate
cd planet
make dev-up
make generate
make run SERVICE=game

# 旧项目执行一次，升级后即可使用 make project-upgrade
go run github.com/tjbdwanghaibo/roost-codegen/cmd/roost@latest project upgrade --root . --dry-run -core latest -kit latest -skill latest -codegen latest
go run github.com/tjbdwanghaibo/roost-codegen/cmd/roost@latest project upgrade --root . -core latest -kit latest -skill latest -codegen latest`,
	},
	{
		Name: "versions", Aliases: []string{"deps", "dependency", "dependencies", "project-deps", "roost-up", "codegen-up"},
		Summary: "持续跟随 core、kit、skill、codegen 最新发布版本",
		Usage: `roost project deps [--root dir]
make deps-update
make roost-up
make codegen-up
roost project upgrade -core latest -kit latest -skill latest -codegen latest`,
		Configuration: fmt.Sprintf(`roost.yaml 的 versions.* 默认是 latest。deps-update 只联合解析 core/kit/skill；roost-up 执行 GOWORK=off go get -u ./... 与 go mod tidy，更新所有被项目引用的依赖；codegen-up 执行 go install github.com/tjbdwanghaibo/roost-codegen/cmd/roost@latest，更新本机 CLI。go.mod 保存具体版本，roost.yaml 保存更新策略。兼容下限：core %s、kit %s、skill %s、codegen %s。明确版本表示 MVS 下限，不是上限。`, minimumVersions.Core, minimumVersions.Kit, minimumVersions.Skill, minimumVersions.Codegen),
		Example: `versions:
  core: latest
  kit: latest
  skill: latest
  codegen: latest

make deps-update
make roost-up
make codegen-up`,
	},
	{
		Name: "generate", Aliases: []string{"gen"},
		Summary:       "按依赖顺序运行项目启用的全部生成器",
		Usage:         `roost generate [--changed] [--dry-run] [--check] [--force] [--root dir]`,
		Configuration: `执行顺序为 DAO → Event → Errcode → Protocol → Entity → Nest → Attribute → Config → WebRoute。--check 在临时副本生成并检查差异；--changed 根据 git 变更缩小范围。`,
		Example: `roost generate --dry-run
roost generate
roost generate --check`,
	},
	{
		Name: "add", Aliases: []string{"scaffold"},
		Summary:       "生成业务骨架并分配需要的 ID",
		Usage:         `roost add <service|mod|module|protocol|entity|component|event|table|dao|webroute|errcode|saga> <name> [flags]`,
		Configuration: `通用参数：--root、--service、--mods、--steps、--group、--id。骨架文件归业务所有，不会被 project sync 覆盖；创建后运行 roost generate。`,
		Example: `roost add entity player
roost add dao player
roost add protocol use_item -group game
roost add saga cross_server_trade -service game -steps reserve,deduct,deliver`,
	},
	{
		Name: "mods", Aliases: []string{"mod", "kit-mods"},
		Summary: "配置并自动补齐 Kit Mod 依赖",
		Usage: `roost add mod <name> -service <service>
roost project new <name> -mods <comma-separated-mods>`,
		Configuration: `可用 Mod：lock、ops、statslog、configdata、etcd、redis、mongo、nats、sync、remote_entity、checkpoint、nestwal、nest、saga。生成器会递归补齐依赖，例如 nest → nestwal → checkpoint/nats → mongo/redis。`,
		Example: `roost add mod saga -service game
# roost.yaml
services:
  game:
    mods: [configdata, etcd, redis, mongo, nats, checkpoint, nestwal, nest, saga]`,
	},
	{
		Name: "dao", Aliases: []string{"database"},
		Summary:       "生成私有存储、getter/mutator、dirty、patch、undo 和持久化代码",
		Usage:         `go run github.com/tjbdwanghaibo/roost-codegen/cmd/dao@latest -def ./db/def -out ./db -pkg db [-force]`,
		Configuration: `定义使用 //cube:dao coll=<collection> db=<database> [dbscope=sid|global]。字段一旦写 dao tag 就必须声明 persist/sync 意图；支持 persist、sync、nopersist、nosync、map=fast、map=sharded 和 -。无 tag 默认 persist+sync。字段与 tracker 均为私有，业务通过生成方法访问。`,
		Example: `//cube:dao coll=players db=game dbscope=sid
type PlayerDao struct {
    Name  string          ` + "`dao:\"persist,sync\"`" + `
    Items map[int64]int32 ` + "`dao:\"persist,sync,map=fast\"`" + `
}

dao.SetName("alice")
dao.SetItems(1001, 3)
name := dao.Name()`,
	},
	{
		Name: "entity", Aliases: []string{"entities"},
		Summary:       "生成 Entity 工厂、组件/DAO 装配、Snapshot 和生命周期 hook",
		Usage:         `go run github.com/tjbdwanghaibo/roost-codegen/cmd/entity@latest -dir ./game/entities [-output file] [-force]`,
		Configuration: `Entity 使用 //cube:entity entityKind=<const>，字段使用 comp:"<type>" 或 dao:"<collection>"。可配置 remote=managed、sync=true、syncTopic、syncPacker、subjectPacker。DAO tracker 统一经 DirtyTracker() 访问。`,
		Example: `//cube:entity entityKind=EntityKindPlayer
type Player struct {
    *entity.EntityBase
    entity.ComponentManager
    entity.DaoManager
    bag *BagComponent ` + "`comp:\"CompTypeBag\"`" + `
    dao *PlayerDao    ` + "`dao:\"players\"`" + `
}`,
	},
	{
		Name: "nest", Aliases: []string{"handler", "sender"},
		Summary:       "生成 Entity 加锁调度、Sender、回滚和 durability 接入",
		Usage:         `go run github.com/tjbdwanghaibo/roost-codegen/cmd/nest@latest -dir ./game [-sender=true] [-force]`,
		Configuration: `handler 使用 //roost:nest；rollback=state|undo，durability=memory|async|strict，可加 sync。Entity 接口参数是锁目标，多个目标按全局顺序加锁。Remote read 放在带 remote tag 的 RemoteViewRef 字段。`,
		Example: `//roost:nest rollback=undo durability=strict
func handlerTransfer(from IPlayerEntity, to IPlayerEntity, itemID int64) error {
    return nil
}`,
	},
	{
		Name: "protocol", Aliases: []string{"proto", "protobuf"},
		Summary:       "生成 Proto、PB Go、消息 ID、binding、handler、robot registry 和 manifest",
		Usage:         `go run github.com/tjbdwanghaibo/roost-codegen/cmd/protocol@latest -def ./protocol/def -proto ./protocol/proto -pb ./protocol/pb -msgid ./protocol/msgid -bind ./protocol/player_bind -handlers ./game/protocol_handlers`,
		Configuration: `文件使用 //roost:proto package=<proto-package> go_package=<go-import;alias>；接口使用 //cube:protocol group=<group> handler=<name>；方法使用 //cube:msg id=<id>。支持 request/response、client push、server notify 和反向 proto 导入。`,
		Example: `//cube:protocol group=game handler=player
type GameProtocol interface {
    //cube:msg id=10001
    Ping(PingRequest) PingResponse
}`,
	},
	{
		Name: "cfggen", Aliases: []string{"configgen", "config-data"},
		Summary:       "从 YAML schema 生成强类型配置表、对象、bean、索引和引用校验",
		Usage:         `go run github.com/tjbdwanghaibo/roost-codegen/cmd/cfggen@latest -meta ./configs/schema/cfg.yaml -out ./configs/generated -pkg generated`,
		Configuration: `schema 支持 package、beans、tables、globals；字段支持 type、key、index、ref、required、skipempty。运行时通过 RegisterGeneratedConfigData 注册到 cube-core/configdata。`,
		Example: `package: generated
tables:
  - name: monster
    key: id
    fields:
      - {name: id, type: int32}
      - {name: scene_id, type: int32, index: true}`,
	},
	{
		Name: "tablegen", Aliases: []string{"table", "csv"},
		Summary:       "从 Go metadata 生成配置类型、CSV 模板并转换/校验 JSON",
		Usage:         `go run github.com/tjbdwanghaibo/roost-codegen/cmd/tablegen@latest -meta ./configs/schema [-out dir] [-csv-template dir] [-csv dir -json dir] [-check] [-force]`,
		Configuration: `类型使用 //cube:table name=<name> file=<csv> json=<json> key=<field> 或 //cube:object。字段通过 csv/json/title/required/unique/ref tag 描述。`,
		Example: `//cube:table name=monster file=monster.csv json=monster.json key=ID
type Monster struct {
    ID   int32  ` + "`csv:\"id\" json:\"id\" required:\"true\" unique:\"true\"`" + `
    Name string ` + "`csv:\"name\" json:\"name\"`" + `
}`,
	},
	{
		Name: "eventgen", Aliases: []string{"event", "events"},
		Summary:       "生成事件类型、Type 方法以及接收者订阅/分发代码",
		Usage:         `go run github.com/tjbdwanghaibo/roost-codegen/cmd/eventgen@latest -def ./event/def -out ./event -pkg event -game ./game -eventpkg <module>/event [-force]`,
		Configuration: `定义目录中 Event 前缀 struct 会进入生成。业务接收者实现 DealEventXxx(*event.EventXxx)；签名不匹配在生成期失败。`,
		Example: `type EventPlayerLevelUp struct { PlayerID int64; Level int32 }

func (p *Player) DealEventPlayerLevelUp(e *event.EventPlayerLevelUp) {}`,
	},
	{
		Name: "attribute", Aliases: []string{"attr", "attributes"},
		Summary:       "生成属性 ID、mask、setter、派生公式、snapshot 和容器访问器",
		Usage:         `go run github.com/tjbdwanghaibo/roost-codegen/cmd/attribute@latest -dir ./game/attribute [-output file] [-force]`,
		Configuration: `profile 使用 //cube:attribute index=<start> max=<count>；字段可用 attr:"name" 或 attr:"-"。_Field 方法定义派生公式，参数名必须匹配输入字段；循环依赖、未知字段和类型不一致会失败。`,
		Example: `//cube:attribute index=1 max=64
type PlayerProfile struct { HP int64; Attack int64; Power int64 }

func (p *PlayerProfile) _Power(Attack int64, HP int64) int64 {
    return Attack*2 + HP/10
}`,
	},
	{
		Name: "webroute", Aliases: []string{"web", "http"},
		Summary:       "生成类型化 HTTP route 注册、请求解码和响应映射",
		Usage:         `go run github.com/tjbdwanghaibo/roost-codegen/cmd/webroute@latest -dir ./service/web [-force]`,
		Configuration: `handler 使用 //cube:web method=<HTTP method> path=<path> body=json|raw。JSON 接受一个完整文档；raw 使用 webroute.RawRequest。重复 method/path、非法模式和错误签名会失败。`,
		Example: `//cube:web method=POST path=/gm/player body=json
func queryPlayer(ctx context.Context, svc *Service, req QueryRequest) (QueryResponse, error) {
    return QueryResponse{}, nil
}`,
	},
	{
		Name: "errcode", Aliases: []string{"error", "errors"},
		Summary:       "扫描错误码定义、检查冲突并导出 CSV",
		Usage:         `go run github.com/tjbdwanghaibo/roost-codegen/cmd/errcode@latest -root . -out docs/generated/errcode.csv`,
		Configuration: `扫描 errcode.Define(code, name, message) 常量调用；code/name 重复或非常量参数会失败。编号空间由 roost.yaml 的 ids.errcode 管理。`,
		Example: `var ErrItemNotFound = errcode.Define(100001, "item_not_found", "item not found")

roost id next errcode
roost generate`,
	},
	{
		Name: "saga", Aliases: []string{"transaction", "cross-service"},
		Summary:       "生成跨服务 Saga 定义、步骤、补偿和注册骨架",
		Usage:         `roost add saga <name> -service <service> -steps <step1,step2,...>`,
		Configuration: `目标 Service 必须启用 saga Mod；它会补齐 nestwal/checkpoint/nats/mongo/redis。每个步骤需要幂等执行与补偿，消息 durable、超时、重试和 receipt 参数位于 saga 配置段。`,
		Example: `roost add mod saga -service game
roost add saga cross_server_trade -service game -steps reserve,deduct,deliver
roost generate`,
	},
	{
		Name: "replication", Aliases: []string{"udp", "kcp", "quic", "lockstep"},
		Summary: "生成 UDP/KCP/QUIC 帧同步 Transport 装配入口",
		Usage: `roost project new <name> -features replication-udp,replication-kcp,replication-quic
roost project sync`,
		Configuration: `feature 生成 internal/transport/generated.go：QUIC DATAGRAM、KCP reliable stream、UDP datagram 与 ReliableSender 组合。监听、TLS、票据、密钥和 Session 绑定由网关接入层提供。`,
		Example: `features:
  - replication-quic
  - replication-kcp
  - replication-udp`,
	},
	{
		Name: "config", Aliases: []string{"config-check"},
		Summary:       "检查服务 YAML 和生产危险默认值",
		Usage:         `roost config check --service <name> [--production] [--file path] [--root dir]`,
		Configuration: `配置必须是合法 YAML 并包含 sid。--production 拒绝 CHANGE_ME、localhost、127.0.0.1 和 dev- 值；外部 Mongo/Redis/NATS 拓扑仍需启动探针验证。`,
		Example: `roost config check --service game
roost config check --service game --production --file configs/service/config.game.prod.yaml`,
	},
	{
		Name: "id", Aliases: []string{"ids", "id-next", "id-check"},
		Summary: "分配并检查协议、Entity、Component 和错误码编号",
		Usage: `roost id next <kind> [--group group]
roost id check`,
		Configuration: `编号范围位于 roost.yaml 的 ids。kind 可使用 protocol、entity、component、errcode；protocol 可按 group 划分范围。`,
		Example: `roost id next -group game protocol
roost id next entity
roost id check`,
	},
	{
		Name: "format", Aliases: []string{"fmt", "format-check"},
		Summary:       "检查仓库 Go 文件是否经过 gofmt",
		Usage:         `roost format check`,
		Configuration: `该命令只检查，不修改文件；修复使用 gofmt 或 make fmt。CI 的 make ci 已包含 fmt-check。`,
		Example: `make fmt
roost format check`,
	},
	{
		Name: "deploy", Aliases: []string{"deployment", "docker", "kubernetes", "k8s", "shell"},
		Summary: "生成 Shell/systemd、Docker 和 Kubernetes 生产部署基线",
		Usage: `roost project sync
make build
docker build -t <app>:<version> .
kubectl apply -k deploy/k8s`,
		Configuration: `Shell 使用版本化 release、校验和、原子切换和 readiness 回滚；Docker 使用 distroless nonroot；K8s 挂载 Secret、配置探针/PDB/NetworkPolicy，带 WAL 的服务使用 StatefulSet 与独占 RWO PVC。`,
		Example: `sh deploy/shell/build.sh
docker build --build-arg VERSION=v1.0.0 -t planet:v1.0.0 .
kubectl apply -f deploy/k8s/secret.game.local.yaml
kubectl apply -k deploy/k8s`,
	},
}

func isHelpToken(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "help", "-h", "--help":
		return true
	default:
		return false
	}
}

func runHelp(args []string, stdout io.Writer) error {
	if err := validateHelpTopics(); err != nil {
		return fmt.Errorf("invalid help catalog: %w", err)
	}
	if len(args) == 0 || (len(args) == 1 && (strings.EqualFold(args[0], "list") || isHelpToken(args[0]))) {
		printHelpOverview(stdout)
		return nil
	}
	if len(args) != 1 {
		return errors.New("usage: roost help [list|all|<capability>]")
	}
	if strings.EqualFold(args[0], "all") {
		for index, topic := range helpTopics {
			if index > 0 {
				fmt.Fprintln(stdout, "\n------------------------------------------------------------")
			}
			printHelpTopic(stdout, topic)
		}
		return nil
	}
	topic, ok := findHelpTopic(args[0])
	if !ok {
		return fmt.Errorf("unknown help capability %q; run roost help to list capabilities", args[0])
	}
	printHelpTopic(stdout, topic)
	return nil
}

func runContextHelp(command []string, stdout io.Writer) error {
	if err := validateHelpTopics(); err != nil {
		return fmt.Errorf("invalid help catalog: %w", err)
	}
	if len(command) == 0 {
		printHelpOverview(stdout)
		return nil
	}
	joined := strings.Join(command, "-")
	for _, candidate := range []string{joined, command[len(command)-1], command[0]} {
		if topic, ok := findHelpTopic(candidate); ok {
			printHelpTopic(stdout, topic)
			return nil
		}
	}
	return fmt.Errorf("no help topic for %q; run roost help to list capabilities", strings.Join(command, " "))
}

func findHelpTopic(name string) (helpTopic, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, topic := range helpTopics {
		if topic.Name == name {
			return topic, true
		}
		for _, alias := range topic.Aliases {
			if alias == name {
				return topic, true
			}
		}
	}
	return helpTopic{}, false
}

func printHelpOverview(w io.Writer) {
	fmt.Fprintln(w, "roost-codegen - Roost 游戏服务器工程与代码生成工具链")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "用法:")
	fmt.Fprintln(w, "  roost help                         显示能力目录")
	fmt.Fprintln(w, "  roost help <能力>                  显示配置、命令和示例")
	fmt.Fprintln(w, "  roost help all                     显示全部专题")
	fmt.Fprintln(w, "  roost <顶级命令> --help            显示对应专题")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "能力:")
	width := 0
	for _, topic := range helpTopics {
		if len(topic.Name) > width {
			width = len(topic.Name)
		}
	}
	for _, topic := range helpTopics {
		fmt.Fprintf(w, "  %-*s  %s\n", width, topic.Name, topic.Summary)
	}
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "示例: roost help dao | roost help nest | roost help versions")
	fmt.Fprintln(w, "完整文档: README.md、docs/CODEGEN_REFERENCE.zh-CN.md、docs/PROJECT_GENERATOR.zh-CN.md")
}

func printHelpTopic(w io.Writer, topic helpTopic) {
	fmt.Fprintf(w, "%s - %s\n\n", topic.Name, topic.Summary)
	fmt.Fprintln(w, "用途:")
	fmt.Fprintln(w, indentHelp(topic.Summary))
	fmt.Fprintln(w, "\n命令:")
	fmt.Fprintln(w, indentHelp(topic.Usage))
	fmt.Fprintln(w, "\n配置/标记:")
	fmt.Fprintln(w, indentHelp(topic.Configuration))
	fmt.Fprintln(w, "\n示例:")
	fmt.Fprintln(w, indentHelp(topic.Example))
}

func indentHelp(value string) string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	for index := range lines {
		lines[index] = "  " + lines[index]
	}
	return strings.Join(lines, "\n")
}

func validateHelpTopics() error {
	seen := make(map[string]string)
	for _, topic := range helpTopics {
		if strings.TrimSpace(topic.Name) == "" || strings.TrimSpace(topic.Summary) == "" || strings.TrimSpace(topic.Usage) == "" || strings.TrimSpace(topic.Configuration) == "" || strings.TrimSpace(topic.Example) == "" {
			return fmt.Errorf("help topic %q is incomplete", topic.Name)
		}
		names := append([]string{topic.Name}, topic.Aliases...)
		for _, name := range names {
			name = strings.ToLower(strings.TrimSpace(name))
			if owner, exists := seen[name]; exists {
				return fmt.Errorf("help alias %q is shared by %s and %s", name, owner, topic.Name)
			}
			seen[name] = topic.Name
		}
	}
	return nil
}

func helpTopicNames() []string {
	names := make([]string, 0, len(helpTopics))
	for _, topic := range helpTopics {
		names = append(names, topic.Name)
	}
	sort.Strings(names)
	return names
}
