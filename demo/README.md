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

## 邮件：列表与领附件（客户端这一半）

升级奖励邮件现在带附件——`game/rewards/` 是附件的编码（一个道具一叠，JSON），发件方（`level_up_mail.go`）与领取方共用它：

```
ListMail 端点  ─ List(playerID, cursor, limit) ─▶ mail 服务（按认证玩家作用域，服务端不需要端点再查归属）
ClaimMail 端点 ─ ReserveClaim(mailID, "") ─▶ mail 服务：交出附件 + 对 (玩家, 邮件) 恒定的 token
               ─ Sync_AddItem(reward)      ─▶ Nest 锁 Player 的一笔事务（与 AddItem 端点同一个 Sender）
               ─ CommitClaim(token)        ─▶ mail 服务：标记已领；重试同 token 幂等
```

- `Claimable` 是 mail 服务对"现在领会成功吗"的回答，客户端不用从 Status 猜（Status 表达不了"被在途投递占着"）。
- 附件解不出奖励、或 AddItem 被拒（背包满）时 `CancelClaim` 归还预留，邮件不会卡在"held"直到租约到期。
- **demo 明确没做的一步**：把 claim token 带进 Nest 事务当幂等键。进程在 AddItem 之后、CommitClaim 之前死掉，租约到期后重试会再发一次
  （mail 侧记成重复 *尝试*，背包却多一叠）。生产做法是在同一事务里把 token 记到 Player 上，第二次拒发。
- 机器人脚本：add_exp 之后 `retry × 20 { wait 250ms; list_mail }`（效果链是异步的：WAL → outbox → JetStream → consumer → mail.Send），
  拿到第一封可领邮件的 id 再 `claim_mail`，断言背包计数 ≥ 领到的数量。

## 跨服务：game → match 组队

match 服务是通用的：队列由 `Queue{Mode, GroupSize, Partition}` 定义、subject 的 kind 对它不透明，它负责的是队列本身与
`Commit` 的原子性——**谁和谁一组不是它决定的**。`Candidates` 交出等待中的票，`Commit` 一次 CAS 成组；决定权是游戏策略，
所以住在 game 进程：

```
JoinQueue 端点 ─ Enqueue(队列, subject, requestID=帧序号) ─▶ match 服务（typed servicerpc，经生成的 Match() 客户端）
internal/service/game/matchmaker.go  每 500ms：Candidates → Grouping.Group → Commit → Sync_RecordMatch(World)
PollMatch 端点 ─ Ticket / Match ─▶ match 服务（带 subject，服务端校验票的归属）
```

- `game/matchmaking/queue.go` 是游戏对 match 说的话：duel 队列两人一组按到达顺序（`FirstComeGrouping`），ranked 队列按等级配（`ScoreWindowGrouping`：
  等级差 5 以内立刻配，窗口随最老候选的等待每秒放宽 5，上限 50）；ticket 的 Score 在 JoinQueue 时经 `PlayerLevel` 读 handler 取（Player 锁内），
  PollMatch 带 `mode` 选队列。subject kind 是 `player`。matchmaker 每 500ms 扫两个 pool，各用自己的策略。
- 所有对 match 的调用都从端点或普通 goroutine 发出，**从不在实体锁里**——World 只在 Commit 成功之后经自己的 Nest handler 记一笔。
- `configs/service/config.match.yaml` 的 `sweep_queues` 列出 duel 队列：match 进程只负责扫过期票，不负责成组。
- `Grouping` 是调用方的工具，不是 match 服务的配置：kit 的 match Mod 曾接受一个 `Grouping` collaborator 却从不执行它
  （RR-20260916-05，已删掉该参数）。demo 的 matchmaker 直接调 `FirstComeGrouping{}.Group`，这是那个接口唯一的用法。

## 跨服务：game → chat 聊天

chat 服务也是通用的：它按频道存带序号的消息、执行这个工程交给它的策略；哪些频道存在、一句话长什么样、谁在线能收到，
是游戏的事，住在 `game/chatroom/`（game 进程与 chat 进程都 import 它——这就是两边对 `text` 这个消息类型名达成一致的方式）：

```
SendChat 端点 ─ Publish(sender=认证玩家, requestID=帧序号) ─▶ chat 服务（typed servicerpc，经生成的 Chat() 客户端）
              └─ deliver：world 频道推给本进程 presence 里的每个在线玩家（含发送者），private 推给双方 ── ChatMessage 推送 10101
EnterGame 端点 ─ PublishSystem(actor=game, "player N entered") ─▶ chat 服务的特权入口 ── 同样 deliver
ChatHistory 端点 ─ History(viewer, after_seq, limit) ─▶ chat 服务（服务端校验读权限）—— 重连补读路径
```

- `internal/service/chat/collaborators.go`：策略写成决定——world（只有 world 1）与 private 对所有认证玩家开放，group 拒绝（demo 没有队伍 /
  公会成员关系），system 频道只读；`Bodies()` 注册唯一的 `text` 类型，校验函数与端点共用（端点先校验，免一次总线往返）；
  `System()` 无条件 `GrantSystem()`——这是对部署的陈述：chat 只经内网 NATS 可达，没有端点转发 PublishSystem。总线对不可信一方可达的部署要换成查调用者身份。
- **推送是捷径，序号是依据**：每条投递都带 `Seq`，错过推送的客户端用 `ChatHistory(after_seq)` 补读——机器人脚本就是这么写的
  （它自己那句的推送可能先于响应到达而被丢，所以 `wait_push` 外面套了 selector，再用 history 断言）。
- `Presence` 是**每进程**的在线集合：EnterGame 加入，推送失败且没有活动会话时移除。多个 game 进程的部署应改为订阅 chat 的流或走 room 总线，而不是从内存扇出。
- 机器人 transport（`loadtest/playertcp/conn.go`）靠帧头的 server-push 标志位识别推送——服务端给推送编的是自己的会话序号，与客户端序号同起点，
  只看序号会把一条世界频道推送当成正在等的响应。

## 跨服务：game → session 副本

session 服务是通用的"有界 run 原语"：每个 owner 同时只有一个活 run、Enter 按 RequestID 幂等、run 有截止时间、附着的资源恰好释放一次。
什么算一局、清了给什么，是游戏的事：

```
EnterDungeon 端点  ─ Enter(playerID, {Kind, RequestID=帧序号}) ─▶ session 服务（typed servicerpc，经生成的 Session() 客户端）
FinishDungeon 端点 ─ Finish(playerID, runID, succeeded|failed, outcome) ─▶ session 服务：状态机 + 释放（调这个工程的 Release()）
                   └─ 清了：MultiSync_AddExp(Player, World, 100) —— 与 AddExp 端点同一笔两实体事务，够升一级，于是又走一遍升级 → 奖励邮件
```

- `internal/service/session/collaborators.go`：`Release()` 是 session 服务对每个附着资源恰好调一次的钩子；demo 的副本不占外部资源，所以是一行日志 + nil。
  真实游戏在这里释放实例 / 座位，返回 error 会让服务保留待释放并在下次 Enter / sweep 重试。
- 第二局要等第一局结束：一个 owner 一个活 run 是服务的契约，重复 Enter 得到 `ErrAlreadyRunning` 的 coded 响应，不是第二个 run。
- **Finish 与发奖不是一个事务**：进程死在两步之间，run 已终态、exp 没给；这是安全的方向——重试 Finish 被拒（`ErrRunTerminal`），exp 至多给一次。
  要"恰好一次"的做法是在 AddExp 事务里把 run id 记到 Player 上再发。
- session 进程默认不扫过期 run（`sweepOwners` 返回空并在日志里说明）：过期 run 由同一 owner 的下一次 Enter 懒解决。要及时释放资源的部署自己接 owner 列表。

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
**`-count` 不要超过 `player_access.tcp.max_connections_per_ip`（默认 128）**：机器人全从一个 IP 来，超出的连接在握手前就被
server 关掉，机器人报 `connect: auth send: robot session: closed`，server 侧计入 `player_tcp_connection_rejected_total{reason}`。
600 个机器人的实跑正好 128 成功、472 这样失败——这是上限在工作，不是缺陷；要压更大就在 game 配置里调高它。

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

`deploy/dev/observability/` 是一套 Prometheus + Grafana：compose、抓取配置（五个进程的 ops 端口——game 9100、account 9101、chat 9102、mail 9103、match 9104，生成的配置已按此分配——+ 压测的 `-metrics-addr`）、
预置数据源与仪表盘 "Roost game-demo"。仪表盘按链路分组：玩家接入 → Nest 分发与锁 → WAL 落库 → 事件链与配置 →
跨服务 RPC → 机器人。每个指标对应链路上的哪一步、该看什么，写在同目录 `README.md`。它与生成的 `deploy/dev/docker-compose.yaml`
分开：那个文件会被重生成，观测是可选的。

## 本地实跑

三条命令（需要 Docker）：

```bash
make dev-up      # deploy/dev/docker-compose.yaml：Mongo 副本集 rs0、NATS JetStream、Redis
make dev-run     # deploy/dev/run.sh start：编译 bin/app，按 account → chat → mail → match → game 起五个进程，等每个 /readyz，
                 # 再用 cmd/accountctl 把 sid 1000 注册进 account（否则 CreateRole 拒绝）；日志与 pid 在 .dev/
make dev-smoke   # 两个机器人走完整条链（登录 → 聊天 → 加道具 → 升级 → 匹配 → 世界计数）
make loadtest LOADTEST_COUNT=20   # 更多机器人；偶数，duel 两人一组
make dev-stop
```

每个服务的 `configs/service/config.<服务>.yaml` 都有自己的 ops 端口（见上），所以五个进程可以同机共存；`make dev-status` 看谁在跑。

没有 Docker、或本机 27017 / 4222 / 6379 已被别的东西占着（一个不带 `--replSet` 的 mongod、一个没开 JetStream 的 nats-server 都不行）时，
用 roost-kit 的隔离环境（`scripts/integration/dataengine-env.sh up`：Mongo 副本集 `roost-it` 27117–27119、NATS JetStream 14222–14224、Redis 16379），
然后把五份配置改指向它——`sed` 一遍即可：

```bash
for f in configs/service/config.*.yaml; do sed -i '' \
  -e 's#mongodb://127.0.0.1:27017/?replicaSet=rs0#mongodb://127.0.0.1:27117,127.0.0.1:27118,127.0.0.1:27119/?replicaSet=roost-it#' \
  -e 's#nats://127.0.0.1:4222#nats://127.0.0.1:14222#' -e 's#^  addr: 127.0.0.1:6379#  addr: 127.0.0.1:16379#' \
  -e 's#^  prefix: roost$#  prefix: mysmoke#' -e 's#max_bytes: 8589934592#max_bytes: 67108864#' "$f"; done
make dev-run
go run ./cmd/loadtest -count 6 -account-nats nats://127.0.0.1:14222 -nats-prefix mysmoke
```

`dataengine.effects.max_bytes` 要调小是因为隔离集群只预留了 1GB 存储，默认 8GB 会报 `insufficient storage`。

看四处：`db.player` 在 DAO 标记写的 `db=game` 库里（不是 `dataengine.database`）；重启 game 再跑一轮，items 与 level 在原值上累加；
mail 的 Redis 里每个升级的玩家一封 `box:<id>`，`send:<EffectID>` 是幂等键；`db.world` 的 `players_entered` / `matches_formed` 随每轮增长；
chat 的 Redis 里 world 频道的流每次登录多一条系统公告、每个机器人多一句 hello。

**给每次实跑一个独立的 `nats.prefix`**（五个进程一致）。共享的 JetStream 集群里若残留了别的测试建的流、
且它的 subject 过滤覆盖 `roost.rpc.>`，JetStream 会用 PubAck 回应每一个 RPC 请求，与真正的服务端抢先——先到的赢，于是
读调用大面积得到 `bus: unsupported rpc response version 0`（PubAck 被当作响应信封解码），写调用偶尔成功。kit
集成测试留下的 `ROOST_IT_RPC_REQ_*` 流就是一例；换前缀即可，无需删流。

**同一个 Mongo 上换工程名 / 换 `nats.prefix` 重跑，game 进程会在就绪后几秒 fail-stop**：`dataengine outbox: hard backlog limit exceeded: oldest_age=… max=30m`。
所有生成工程的 `dataengine.database` 默认都是 `game`，上一轮工程留下的 effect outbox 行没有消费者会 ack（前缀不同、流不同），超过 30 分钟就触发
Data Engine 的硬积压熔断——这是它该有的行为（积压不该静默增长），不是 demo 的 bug。处置：`mongosh --eval 'db.getSiblingDB("game").dropDatabase()'`，
或给每个工程改 `dataengine.database`。

**连续两次压测间隔不到 60s 时可能有一个机器人 `poll_match: ticket still waiting`**：上一轮失败机器人的 duel 票还在队列里（TicketTTL 60s），
被这一轮的第一个机器人配走了，剩下奇数个。这是 match 服务的正确行为，不是 bug；等 sweep 把过期票清掉再跑，或起偶数个再加一个。

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
