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
