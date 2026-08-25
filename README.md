# roost-codegen

Roost 项目生成器和通用 Go 代码生成器。

## 创建项目

    go run github.com/tjbdwanghaibo/roost-codegen/cmd/project@v1.4.0 planet \
      -services game,gate \
      -mods configdata,etcd,redis,mongo,nats,sync,remote_entity

也可以使用统一入口：

    go run github.com/tjbdwanghaibo/roost-codegen/cmd/roost@v1.4.0 \
      project new planet

生成项目使用 roost.yaml 描述 Service、Kit Mod、生成能力、版本和 ID 空间。

Windows 下安装命令：

    powershell -ExecutionPolicy Bypass -File .\scripts\install-windows.ps1
    roost help

脚本不会修改现有 `GOBIN` 或 `GOPATH`，只会把 `go install` 实际使用的
Go bin 目录追加到用户 PATH。完整步骤见
[Windows 安装与 PATH](docs/PROJECT_GENERATOR.zh-CN.md#windows-安装与-path)。

## 统一命令

    roost project new <name>
    roost project sync
    roost project diff
    roost project doctor
    roost project upgrade
    roost generate
    roost generate --changed
    roost generate --check
    roost add service|mod|module|protocol|entity|component|event|table|dao|webroute|errcode|saga
    roost config check
    roost id next|check

Saga 脚手架：

    roost add mod saga -service game
    roost add saga AllianceRally -service game -steps ReserveTroops,CreateMarch,CreateRally

完整说明见 [项目生成器使用说明](docs/PROJECT_GENERATOR.zh-CN.md)。

## 独立生成器

所有命令会从目标目录自动发现 Go module：

    go run github.com/tjbdwanghaibo/roost-codegen/cmd/protocol@v1.4.0 -def ./protocol/def -force
    go run github.com/tjbdwanghaibo/roost-codegen/cmd/webroute@v1.4.0 -dir ./service
    go run github.com/tjbdwanghaibo/roost-codegen/cmd/attribute@v1.4.0 -dir ./game/gameplay/attribute -force
    go run github.com/tjbdwanghaibo/roost-codegen/cmd/dao@v1.4.0 -def ./db/def -out ./db
    go run github.com/tjbdwanghaibo/roost-codegen/cmd/entity@v1.4.0 -dir ./game
    go run github.com/tjbdwanghaibo/roost-codegen/cmd/errcode@v1.4.0 -root . -out docs/generated/errcode.csv
    go run github.com/tjbdwanghaibo/roost-codegen/cmd/eventgen@v1.4.0 -def ./event/def -out ./event -game ./game
    go run github.com/tjbdwanghaibo/roost-codegen/cmd/nest@v1.4.0 -dir ./game
    go run github.com/tjbdwanghaibo/roost-codegen/cmd/tablegen@v1.4.0 -meta ./configs/schema -out ./configs/generated

DAO 生成结果使用私有存储字段。业务读取标量和嵌套对象使用 `Get<Field>()`，读取
slice 使用 `Get<Field>(index)` / `Range<Field>` / `<Field>Len`，读取 map 使用
`Get<Field>(key)` / `Range<Field>` / `<Field>Len`；写入仍统一经过生成的 mutator，
从而不能通过字段赋值绕过 dirty、rollback 与同步语义。这是有意的源码不兼容变更，
升级后应重新生成 DAO 并把直接字段访问迁移到方法调用。

新项目的 Nest handler 使用显式 capability 标记：

```go
type BagOwner interface { Bag() BagComponent }

//roost:nest target=player sync
func handlerUseItem(owner BagOwner, itemID int64) error { /* ... */ }
```

未声明时生成器默认使用 `rollback=state durability=async`，业务修改会先进入 Nest WAL。需要请求返回前确认 durable admission 时声明：

```go
//roost:nest target=player sync rollback=undo durability=strict
func handlerTransfer(owner BagOwner, target TargetOwner, itemID int64) error { /* ... */ }
```

`rollback` 支持 `state/undo`，热路径推荐 `undo`；`durability` 支持 `memory/async/strict`。只有明确不持久化的 handler 才应显式使用 `durability=memory`。重新生成的 DAO 会实现无副作用 rollback snapshot、字段级 undo 和 Nest commit participant。

生成的 `sender` / `syncsender` 包同时包含可注入的 `*Sender` 类型。接入层应通过
`NewBagSender(nestClient)` 构造 Sender，并调用带 `context.Context`、可返回入队错误
的方法。生成器不再生成包级 Send/Sync，也不读取全局 `nest.Nest`。完整说明见
[Nest 业务执行模型](docs/NEST_RUNTIME.zh-CN.md)。
