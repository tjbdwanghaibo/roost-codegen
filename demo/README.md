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
  → game/controllers/player/add_item.go      端点（生成，直接可用）
  → game/handler/sender/…Sync_AddItem         Sender（从 handler 参数生成）
  → Nest 锁住 Player
  → game/handler/add_item.go                  handlerAddItem（本目录）
  → BagComponent.AddItem                      本目录，只走生成的 mutator
  → PlayerDao.SetItems                        生成的 map mutator，带 dirty / undo / patch
  → dataengine 落库
```

几个刻意保留的教学点：

- **业务代码只碰生成的 mutator**（`dao.SetItems(...)`），不直接写私有字段——dirty 追踪、Nest undo 和持久化
  patch 全靠它。
- **handler 参数与协议字段必须对上**：`add endpoint` 会解析两边并拒绝对不上的组合，所以
  `game/handler/add_item.go` 的 `(itemID int64, count int32)` 与 `protocol/def/add_item.go` 的
  `ItemID` / `Count` 是同一份契约。
- **`rollback=undo durability=strict`**：handler 返回 error 时本次调用过的 mutator 全部回滚。

## auth.go 不是认证

`internal/access/player/tcp/auth.go` 认一个 `player:<id>` 的字符串就把你当成那个玩家。它的存在只是为了让
demo 能在终端里跑通，**上线前必须换成真实校验**（签名、过期、密钥来自配置或 Secret）。
生成器默认给的是 fail-closed 骨架，demo 故意替换掉它——这也是 `roost config` 启用 TCP 时会检查的那个文件。

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

   codegen HEAD 生成的实体接线依赖 core HEAD（entity category），所以本地要用 `go.work` 指到工作树的
   core / kit，不能只用已发布 tag。
