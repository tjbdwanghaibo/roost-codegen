# `-template game-demo` 的源码

`roost project new <name> -module <mod> -template game-demo` 在 `game` 模板之上再生成**一条可运行的写入链路**。
这个目录就是它写进项目的那些文件，**按生成后的真实路径原样存放**（多一个 `.tmpl` 后缀）。

## 为什么是文件而不是 Go 字符串

codegen 的其他模板是 `fmt.Sprintf` 出来的字符串字面量。demo 的体量大、要被人照着改，所以存成真文件：
能直接读、能 diff、能整段 review，不用在字符串里数 `%q`。

`.tmpl` 后缀让它们不参与 codegen 自身的编译——内容 import roost-core，而 **codegen 对运行时零依赖**
（见 `go.mod`，只有 `gopkg.in/yaml.v3`）。

正确性不靠在这里编译，而是靠 **CI 生成一个项目再编译它**（`framework-compat.yml` 的 `demo` scenario）。
roost-core 自己的 `examples/` 模块就是反例：不在任何 CI 里，`go.sum` 过期之后静静地编译不过了。

## 占位符

| 占位符 | 替换成 |
| --- | --- |
| `{{MODULE}}` | 项目的 Go module 路径 |

## 链路

```
TCP (player access)
  → game/controllers/player/add_item.go      端点（本目录）：错误边界，coded error → 响应里的 Code/Reason
  → game/handler/syncsender/…Sync_AddItem     Sender（从 handler 参数与返回值生成）
  → Nest 锁住 Player
  → game/handler/add_item.go                  handlerAddItem（本目录）
  → BagComponent.AddItem                      本目录：查 item 表 → 校验 → 只走生成的 mutator
      ↳ configs/generated.ItemByID             生成的表访问器，读的是钉在本次请求上的配置快照
      ↳ internal/errors.Err*                   errcode.Define，code 在清单的 errcode 号段里
  → PlayerDao.SetItems                        生成的 map mutator，带 dirty / undo / patch
  → dataengine 落库
```

几个刻意保留的教学点：

- **业务代码只碰生成的 mutator**（`dao.SetItems(...)`），不直接写私有字段——dirty 追踪、Nest undo 和持久化
  patch 全靠它。
- **handler 参数与协议字段必须对上**：`add endpoint` 会解析两边并拒绝对不上的组合，所以
  `game/handler/add_item.go` 的 `(itemID int64, count int32)` 与 `protocol/def/add_item.go` 的
  `ItemID` / `Count` 是同一份契约。handler 的第一个返回值（新数量）经 Sender 原样带回端点。
- **`rollback=undo durability=strict`**：handler 返回 error 时本次调用过的 mutator 全部回滚。
- **错误在端点换形状**：生成的 TCP server 把"端点返回 error"当作坏帧处理——断连。所以业务失败不能作为
  error 返回，而是 `errcode.ClientError(err)` 换成 `(code, reason)` 写进响应；没有 code 的错误（基础设施故障、
  bug）统一坍缩成 `CodeInternal` + 固定文案，内部信息不出网，原因在端点处打日志。
- **配置表走生成器**：`configs/schema/item.go` 的 `//roost:table` 是唯一手写的地方；`roost generate` 生成类型化
  loader、把 `configs/table/item.csv` 转成 `configs/data/item.json`（CSV 前四行是列名 / 标题 / 类型 / 规则），
  并注册进 `roost-core/configdata`。`required` / `unique` 在转换时校验，坏数据死在 `make generate`，
  不会死在玩家请求里。
- **错误码归号段**：`roost add errcode X -id N` 的 N 必须落在清单 `ids.errcode`（100000–199999）里，
  `roost id check` 负责查重；`docs/generated/errcode.csv` 是给客户端的对照表。

## 事件链：升级 → 奖励邮件（事务性 outbox）

```
AddExp 端点 → Nest 锁 Player → ProfileComponent.AddExp
  ├─ dao.SetExp / SetLevel                 状态变更
  └─ effects.EmitPlayerLevelUp             nest.Emit：effect 与状态变更进同一条 WAL 记录
        ↓ commit 后由 dataengine 发到 JetStream（<subject_prefix>.player.level_up）
internal/service/game/level_up_mail.go     durable consumer + Mongo inbox（每个 EffectID 只处理一次）
  └─ mail.Send(RequestID = EffectID)       mail 服务按 RequestID 去重：第二层幂等
```

- **升级"这件事"在组件里发出**，紧挨着让它成立的状态变更；谁对它做出反应住在别处。handler 回滚时 effect 一起消失，
  所以永远不会给一个没落库的升级发奖励。
- **两层幂等**：inbox 收据与业务写入在一个 Mongo 事务里提交，进程重启后重投的 effect 会被认出来；邮件是总线调用
  不是 Mongo 写入，所以还要靠 `RequestID = EffectID` 让 mail 服务自己去重。
- **生产者与消费者读同一组配置键**（`dataengine.database` / `dataengine.effects.*`）、用同一组默认值，两边不会对"effect 在哪"
  产生分歧。
- 消费者在 `Service.Init` 里订阅、`Shutdown` 里 `Drain`：进程宕机期间提交的升级，回来时会补投。

## 跨服务：game → match 组队

match 服务是通用的：队列由 `Queue{Mode, GroupSize, Partition}` 定义、subject 的 kind 对它不透明，它负责的是队列本身与
`Commit` 的原子性——**谁和谁一组不是它决定的**。`Candidates` 交出等待中的票，`Commit` 一次 CAS 成组；决定权是游戏策略，
所以住在 game 进程：

```
JoinQueue 端点 ─ Enqueue(队列, subject, requestID=帧序号) ─▶ match 服务（typed servicerpc，经生成的 Match() 客户端）
internal/service/game/matchmaker.go  每 500ms：Candidates → Grouping.Group → Commit → Sync_RecordMatch(World)
PollMatch 端点 ─ Ticket / Match ─▶ match 服务（带 subject，服务端校验票的归属）
```

- `game/matchmaking/queue.go` 是游戏对 match 说的话：duel 队列两人一组、subject kind 是 `player`。
- 所有对 match 的调用都从端点或普通 goroutine 发出，**从不在实体锁里**——World 只在 Commit 成功之后经自己的 Nest handler 记一笔。
- `configs/service/config.match.yaml` 的 `sweep_queues` 列出 duel 队列：match 进程只负责扫过期票，不负责成组。
- 一个发现：kit 的 match Mod 接受 `Grouping` collaborator 并写明"这是整个匹配策略"，但 store 里没有任何路径调用它；
  成组是调用方驱动的。demo 的 matchmaker 直接调 `FirstComeGrouping{}.Group`，这才是那个接口的用法。

## World 的职责

World 有了自己的 DAO（`PlayersEntered` / `MatchesFormed`）和 `Stats` 组件：`RecordEnter` 在 EnterGame 之后、
`RecordMatch` 在 Commit 之后各是一次独立的 Nest 调用（Player 与 World 是不同实体、不同锁档）；`WorldStats` 是一个带返回值的
读 handler，锁内读、值出锁，端点从不碰 World 本身。

## 机器人压测 = 回归测试

```bash
go run ./cmd/accountctl -redis 127.0.0.1:6379 upsert-server -sid 1000   # 环境准备时一次：account 得知道这台服
make loadtest LOADTEST_COUNT=20                                           # 等价于下面这行
go run ./cmd/loadtest -endpoint 127.0.0.1:7000 -count 20 -metrics-addr 127.0.0.1:9300 -account-nats nats://127.0.0.1:4222
```

机器人走真实凭据：在压测进程里起一条 bus，用 account 的 typed 客户端依次 `Login`（demo 渠道，凭据 `demo:<open_id>`）→
`CreateRole`（在 `-server-id` 上）→ `SelectRole`，再以 `session:<player_id>:<token>` 握手；每次运行用新的 open id
（账号在一个服务器上只能有一个角色，且没有角色列表可查）。

`CreateRole` 会拒绝未知或未开放的服务器，而 **`UpsertServer` 刻意不在 account 的 RPC 接口上**：登记 / 开关服务器改变的是
所有玩家能登录什么，game 进程无权做，放在 Login 同一条总线上等于任何能到达 account 的进程都能关服。所以 demo 给了一个
操作员工具 `cmd/accountctl`：用 Redis 凭据直接打开 account 服务自己的 store 写入服务器记录——
`go run ./cmd/accountctl -redis 127.0.0.1:6379 upsert-server -sid 1000`，环境准备时跑一次。

每个机器人：`connect`（握手凭据是 account 签发的票据）→ `enter_game`（GetOrCreate Player）→ `add_item` → `add_exp`（升级，触发奖励邮件）
→ `join_queue` → `wait_push` 等服务端推送的 `MatchFound`（msg 10100，10s 超时后退回每 250ms `poll_match`，`selector` 节点）
→ `world_stats`（两个计数都得大于零）。
任何一步返回非零 code、或 `error_rate` / `p95` 超阈值，进程以非零码退出并打印 JSON 报告。**`-count` 要给偶数**：duel 两人一组。

- **runner / 场景树 / 动作注册 / 阈值门**全是 `roost-core/robot`，`cmd/loadtest/main.go` 只做三件事：注册本工程的消息
  （`action.MustRegisterCall` + 一个把泛型 Marshal 路由到生成的 pb 函数的 codec）、加载 `loadtest/scenarios/*.yaml`、
  把 flag 变成一个 `loadtest.Profile`。
- **`loadtest/playertcp`** 是生成的 player TCP 帧的客户端半边：core 的 robot 传输层说的是 12 字节小端帧，生成的 server
  说的是 16 字节大端带 magic / 版本 / flags 的帧，且要求先握手、每帧序号严格递增非零。适配器自己计数 wire 序号，
  用一张表把响应映射回机器人等待的序号；这是唯一同时知道两边格式的地方，常量要与 `server_gen.go` 同步。
- **`enter_game` 为什么不是 Nest handler**：Nest 处理的是已存在的实体，第一次登录还没有；创建 Player 是生命周期操作，
  端点直接走 `PlayerLifecycle.GetOrCreate`，所以这条协议只有 `roost add protocol`，控制器方法手写。
- 生成的 pb 类型没有 `GetCode()`，`RegisterCall` 的自动 code 检查不会生效，每个 call 用 `OnResp` 自己查 `Code`。
- **成组后推送**：matchmaker 在 Commit 成功后经传输层 `Runtime.PushPlayer` 给每个成员推 `MatchFound`（协议里是一个没有请求的
  notify 方法，生成的 bind 注册它的编码器）。推送到达的是该玩家**所有**已认证会话；玩家已下线时推送失败只记 debug 日志——票据里
  仍有 match_id，`PollMatch` 是兜底，推送是捷径而不是事实来源。

## 可观测性

`deploy/dev/observability/` 是一套 Prometheus + Grafana：compose、抓取配置（三个进程的 ops 端口 + 压测的 `-metrics-addr`）、
预置数据源与仪表盘 "Roost game-demo"。仪表盘按链路分组：玩家接入 → Nest 分发与锁 → WAL 落库 → 事件链与配置 →
跨服务 RPC → 机器人。每个指标对应链路上的哪一步、该看什么，写在同目录 `README.md`。它与生成的 `deploy/dev/docker-compose.yaml`
分开：那个文件会被重生成，观测是可选的。

## 本地实跑

用 roost-kit 的隔离环境（`scripts/integration/dataengine-env.sh up`：Mongo 副本集 27117–27119、NATS JetStream 14222–14224、Redis 16379）
跑过整条链。在生成的工程里复制一份 `configs/service/config.game.yaml`，改四处：`mongo.uri` 指向副本集、`nats.url`、
`dataengine.effects.max_bytes` 调小（隔离集群只预留了 1GB 存储，默认 8GB 会报 `insufficient storage`）、`player_access.tcp.addr`；
mail 的配置同样改 `redis.addr` 与 `nats.url`。然后：

```bash
go run . game --sid 1000 --config configs/service/config.game.smoke.yaml &
go run . mail --sid 1000 --config configs/service/config.mail.smoke.yaml &
go run ./cmd/loadtest -endpoint 127.0.0.1:7000 -count 5
```

看四处：`db.player` 在 DAO 标记写的 `db=game` 库里（不是 `dataengine.database`）；重启 game 再跑一轮，items 与 level 在原值上累加；
mail 的 Redis 里每个升级的玩家一封 `box:<id>`，`send:<EffectID>` 是幂等键；`db.world` 的 `players_entered` / `matches_formed` 随每轮增长。

**给每次实跑一个独立的 `nats.prefix`**（三个进程一致，例如 `planetsmoke`）。共享的 JetStream 集群里若残留了别的测试建的流、
且它的 subject 过滤覆盖 `roost.rpc.>`，JetStream 会用 PubAck 回应每一个 RPC 请求，与真正的服务端抢先——先到的赢，于是
读调用大面积得到 `bus: unsupported rpc response version 0`（PubAck 被当作响应信封解码），写调用偶尔成功。这一次就是 kit
集成测试留下的 `ROOST_IT_RPC_REQ_*` 流；换前缀即可，无需删流。

## account 的 collaborators

`game` 模板给的 `Verifier` / `Allocator` 默认全拒绝（这是对的：没有校验的身份和会重复的 id 都不该有默认值）。
demo 换成能跑的版本：

- `Verifier` 只认 `demo` 渠道，凭据必须是 `demo:<open_id>`，其它渠道一律 fail-closed，返回的是它确认过的身份而不是提交
  上来的那份。**这不是身份校验**，只是把"该做的两件事"做了个样子，上线前换成对平台的真实调用。
- `Allocator` 用 account 服务自己 Redis 里的一个 `INCR` 计数器——持久、跨副本共享，满足 allocator 契约。它通过
  `account.RegistryBound`（roost-kit）在 `Provide` 里拿到 registry 再查 Redis 客户端：collaborator 是在 app 存在之前
  构造的，没有这个钩子就拿不到任何持久的东西。

## auth.go：会话票据

`internal/access/player/tcp/auth.go` 只认 `session:<player_id>:<token>`：token 由 account 服务签发（Login → CreateRole → SelectRole），
这里经 account 客户端 `ValidateSession` 校验，principal 用的是 account 返回的角色而不是 socket 声称的 id。拿到 account 客户端靠
生成的 TCP 传输层的 `RegistryBound` 钩子：authenticator 在 `Init` 里只有 viper 配置，Mod 在 `Provide` 里把 registry 交给它。
**没有调试捷径**：服务端不校验的凭据不是凭据，压测也走真实登录（下文）。生成器默认给的是 fail-closed 骨架，demo 替换掉它——
这也是 `roost config` 启用 TCP 时会检查的那个文件。脚手架同时把 `player_access.tcp.enabled` 置为 true，
`roost project doctor -workflow player-tcp` 在刚生成的工程上全绿。

## 实体锁档与跨实体事务

`entity.EntityCategory` 的值就是锁的获取顺序（低的先锁）：Remote(1) → World(2) → PlayerScoped(3) → Player(4) → Other(5)。
生成器默认把新实体放在 Other——"持有它之后什么都锁不了"，对还没决定顺序的实体是安全的。demo 把 Player 放到
`EntityCategoryPlayer`、World 放到 `EntityCategoryWorld`，因为 `AddExp` 是一个**两实体事务**：
`handlerAddExp(target player.IProfileEntity, stats world.IStatsEntity, amount int64)`——Player 加经验升级、World 累计
`ExpGranted`，两处变更进同一条 WAL 记录，要么都落库要么都不。Nest 按档位锁：World 先、Player 后。生成的 Sender 变成
`MultiSync_AddExp(ctx, target, stats, amount)`，一个实体一个 id；`roost add endpoint` 只接单实体 handler，所以这个端点手写。

**档位编进实体 id**：改一个 kind 的 category 会改它所有实体的 id，已落库的文档全部失配。所以这是在第一条文档落库之前
决定一次的事；之后再改等于一次数据迁移。

## 可观测性

`deploy/dev/observability/` 是一套 Prometheus + Grafana：compose、抓取配置（三个进程的 ops 端口 + 压测的 `-metrics-addr`）、
预置数据源与仪表盘 "Roost game-demo"。仪表盘按链路分组：玩家接入 → Nest 分发与锁 → WAL 落库 → 事件链与配置 →
跨服务 RPC → 机器人。每个指标对应链路上的哪一步、该看什么，写在同目录 `README.md`。它与生成的 `deploy/dev/docker-compose.yaml`
分开：那个文件会被重生成，观测是可选的。

## 本地实跑

用 roost-kit 的隔离环境（`scripts/integration/dataengine-env.sh up`：Mongo 副本集 27117–27119、NATS JetStream 14222–14224、Redis 16379）
跑过整条链。在生成的工程里复制一份 `configs/service/config.game.yaml`，改四处：`mongo.uri` 指向副本集、`nats.url`、
`dataengine.effects.max_bytes` 调小（隔离集群只预留了 1GB 存储，默认 8GB 会报 `insufficient storage`）、`player_access.tcp.addr`；
mail 的配置同样改 `redis.addr` 与 `nats.url`。然后：

```bash
go run . game --sid 1000 --config configs/service/config.game.smoke.yaml &
go run . mail --sid 1000 --config configs/service/config.mail.smoke.yaml &
go run ./cmd/loadtest -endpoint 127.0.0.1:7000 -count 5
```

看四处：`db.player` 在 DAO 标记写的 `db=game` 库里（不是 `dataengine.database`）；重启 game 再跑一轮，items 与 level 在原值上累加；
mail 的 Redis 里每个升级的玩家一封 `box:<id>`，`send:<EffectID>` 是幂等键；`db.world` 的 `players_entered` / `matches_formed` 随每轮增长。

**给每次实跑一个独立的 `nats.prefix`**（三个进程一致，例如 `planetsmoke`）。共享的 JetStream 集群里若残留了别的测试建的流、
且它的 subject 过滤覆盖 `roost.rpc.>`，JetStream 会用 PubAck 回应每一个 RPC 请求，与真正的服务端抢先——先到的赢，于是
读调用大面积得到 `bus: unsupported rpc response version 0`（PubAck 被当作响应信封解码），写调用偶尔成功。这一次就是 kit
集成测试留下的 `ROOST_IT_RPC_REQ_*` 流；换前缀即可，无需删流。

## account 的 collaborators

`game` 模板给的 `Verifier` / `Allocator` 默认全拒绝（这是对的：没有校验的身份和会重复的 id 都不该有默认值）。
demo 换成能跑的版本：

- `Verifier` 只认 `demo` 渠道，凭据必须是 `demo:<open_id>`，其它渠道一律 fail-closed，返回的是它确认过的身份而不是提交
  上来的那份。**这不是身份校验**，只是把"该做的两件事"做了个样子，上线前换成对平台的真实调用。
- `Allocator` 用 account 服务自己 Redis 里的一个 `INCR` 计数器——持久、跨副本共享，满足 allocator 契约。它通过
  `account.RegistryBound`（roost-kit）在 `Provide` 里拿到 registry 再查 Redis 客户端：collaborator 是在 app 存在之前
  构造的，没有这个钩子就拿不到任何持久的东西。

## auth.go：两种凭据

`internal/access/player/tcp/auth.go` 认两种字符串：

- `session:<player_id>:<token>` —— **真实路径**。token 由 account 服务签发（Login → CreateRole → SelectRole），
  这里经 account 客户端 `ValidateSession` 校验，principal 用的是 account 返回的角色而不是 socket 声称的 id。
  客户端拿到 account 客户端靠生成的 TCP 传输层新增的 `RegistryBound` 钩子：authenticator 在 `Init` 里只有 viper 配置，
  Mod 在 `Provide` 里把 registry 交给它。
- `player:<id>` —— **不是认证**，直接信任 socket 给的 id，只为了能用一个裸 TCP 客户端把 demo 跑通。上线前删掉
  `demoTokenPrefix` 和读它的分支。

生成器默认给的是 fail-closed 骨架，demo 故意替换掉它——这也是 `roost config` 启用 TCP 时会检查的那个文件。
脚手架同时把 `player_access.tcp.enabled` 置为 true（等价于 `roost config enable player-tcp`），
所以 `roost project doctor -workflow player-tcp` 在刚生成的工程上全绿。

## 改这里的东西之后

1. `go test ./internal/roost/ -run TestDemo` —— 嵌入清单、步骤与文件一致性、生成后可解析。
2. 真正的验收是 CI 的 `demo` scenario：生成 + `go build` + `go vet` + `go test`。
   本地等价做法：

   ```bash
   go build -o /tmp/roost ./cmd/roost
   /tmp/roost project new planet -module example.com/planet -out /tmp/planet \
     -mods configdata,mongo,nats,dataengine,nest -template game-demo
   cd /tmp/planet && go build ./...
   ```

   codegen HEAD 生成的实体接线依赖 core HEAD（entity category），account collaborators 依赖 kit HEAD
   （`account.RegistryBound`），所以本地要用 `go.work` 指到工作树的 core / kit，不能只用已发布 tag。
   生成后再跑 `roost id check` 与 `roost generate --check`，确认 demo 留下的是一份干净的生成态。
