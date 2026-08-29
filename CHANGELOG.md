# Changelog

本文件从 v1.5.0 起维护；更早版本见 git 历史。

## [Unreleased]

### Changed
- `project sync` 改为临时项目中完成模板和生成器预演，成功后再以可回滚批次提交，并正确提交生成器判定的孤儿文件删除；`add service/mod/access/transport/saga` 的 manifest、生成文件与 TCP 双配置在失败时恢复，避免半完成工程。
- 同步/升级提交增加乐观并发校验，提交或回滚期间检测到另一进程/开发者编辑时拒绝覆盖；`roost generate` 也改为临时工程完成全部生成器与不升级版本的 `go mod tidy` 后批量提交，业务输入在运行期间发生变化会拒绝提交。依赖更新同样改为临时工程解析，只提交经过校验的 `go.mod/go.sum`。
- 修复生成事务同时从模板计划和 tidy 计划登记 `go.mod/go.sum` 时产生的重复提交与伪并发冲突；提交计划现在按目标去重并拒绝内容矛盾的重复项。
- Component/DAO 与 Entity 的联合接线、endpoint/controller、Skill catalog 等多文件脚手架统一使用带并发校验的可回滚批次；关联名称、Go module path、显式 ID 范围和重复 ID 均在写文件前 fail-fast，阻止路径穿越、声明注入和越界编号。
- Entity、Nest、Event、Table 四个内嵌生成器移除全局 flag/输出状态与 `log.Fatal` 退出路径，非法参数或业务定义错误统一返回给 roost，由上层完成回滚和清理；进程级 cwd 切换增加串行保护，独立命令的 `-h` 保持成功退出。
- first-business workflow 现在拒绝空 DAO、Component 骨架的 `Name()`、未调用所属 Component 的 Handler，以及与 Handler 参数不匹配的 Request；DAO scaffold 不再预置业务字段，保持无 preset。
- Player TCP 增加独立鉴权帧上限、鉴权并发上限、单 IP 连接上限、出站 payload/保留 message ID 校验和 Stop/Accept 栅栏；握手总耗时与调用方写 deadline 均有界，session 读路径使用 RWMutex。Docker 与 Kubernetes 自动为实际所属 Service 声明 player 7000 端口，NetworkPolicy 默认只允许显式标记的调用方命名空间。
- CLI 要求新项目显式传 `-module`，拒绝多余位置参数和子命令无关 flag；新增 `roost version`、`roost env doctor`，所有业务 scaffold 统一回到唯一的 `project next`。
- 新手流程改为状态驱动：`project next` 根据真实项目只输出当前一个安全动作；first-business doctor 进一步拒绝只生成未实现的 Component、仍 `return nil` 的 Nest handler 和空 Request，避免“结构齐全但没有业务”的假完成。
- `add transport tcp` 现在增量补齐开发配置与生产示例的 disabled TCP 配置，保持其他 YAML 文本和注释；`config enable player-tcp` 在鉴权仍为默认拒绝骨架时拒绝开端口，`disable` 提供安全停流。鉴权构造器在 Mod Init 时接收 Viper，便于从 Secret/config 初始化 verifier。
- DAO 生成物的 BSON import 与当前 core/kit 统一为 `go.mongodb.org/mongo-driver/v2/bson`，不再意外引入已淘汰的 Mongo Driver v1 模块。
- 默认 EntityKind ID 空间修正为 core 编码允许的 1..255，并对 EntityKind/ComponentType/Protocol 的底层位宽增加 manifest fail-fast 校验；旧的 1000..1999 EntityKind 默认区间会生成无法编译的 uint8 常量。
- `roost add dao` 统一生成 `<Name>Dao` 类型，修复 Entity wire 期待 `PlayerDao/NewPlayerDao` 而 DAO 实际生成 `Player/NewPlayer` 的跨生成器编译错误。
- `roost add dao` 的默认骨架不再生成任何业务字段，也不生成与框架私有 `id` 冲突的 `ID` 字段；注释给出字段写法并明确禁止业务声明 `ID/id/tracker` 保留字段。
- 生成代码统一导入稳定的 `github.com/tjbdwanghaibo/roost-skill/skill`，删除 `/skillv2` 路径；相应把 roost-skill 兼容下限提高到首个提供稳定包的 v1.9.0。
- `roost add handler` 现在同时校验 nest 生成能力和服务运行时 Mod，避免新手生成可以编译但缺少 WAL/checkpoint 装配的半成品；零基础教程给出完整的 `add mod`、基础设施启动和服务启动顺序。
- 项目版本策略默认改为 core、kit、skill、codegen 全部跟随 `latest`；明确版本只要不低于兼容下限即可。
- release-hygiene 增加根模块 `replace` 禁止门禁，防止本地联调路径进入发布 tag；CI 显式监听 `v*` tag，确保 tag/module-major 校验不再是不可达步骤。
- 新项目 `go.mod` 直接 require 三个真实发布 module，不再生成 `v0.0.0 + replace`；`project new/sync/deps` 联合执行 core、kit、skill 的 `@latest` 解析并 `go mod tidy`，失败时恢复原 go.mod/go.sum。新增无运行时副作用的 `internal/frameworkdeps` 类型别名，保证尚未被业务使用的 skill 在 tidy 后仍保留为直接依赖。
- Entity generator 的 checkpoint、删除 tombstone 和 Remote Entity 路径统一改用 DAO `DirtyTracker()` 方法，不再访问已经私有化的 `Tracker` 字段；DAO 与 Entity 生成物恢复同代可编译。

### Added
- 新增 `roost project next [--workflow first-business|player-tcp]`、生成 Makefile 的 `next`/`player-tcp-enable`/`player-tcp-disable`，以及根仓库和生成项目双份 `BEGINNER_WORKBOOK.zh-CN.md`；覆盖安装、文件所有权、首条业务每个编辑点、TCP 鉴权、主动推送、启动、固定排错顺序和发布清单。
- 第二阶段新增可组合的 `roost add transport tcp`：在已有 `access.player` 上生成标准库 TCP listener、16 字节有界帧、连接硬上限、握手/空闲/写超时、sequence 重放拒绝、小包复用、同步写背压、TCP keepalive/no-delay 和 context 优雅关停；生成的应用鉴权默认 fail-closed 且不会被 sync 覆盖。Transport Runtime 提供一次编码、多会话发布的 `PushPlayer`/`PushSession`，推送 flag 与服务端 sequence 不占用请求序列。新增配置示例、帧/限包/推送生成测试、`new-transport` Make 入口、`roost help transport`、`project doctor --workflow player-tcp` 与 PLAYER_ACCESS_TCP 上线文档。
- 新增显式首层业务工作流，不引入 preset：`roost add access player` 生成 transport-neutral 玩家协议 Registry 与 Service Mod；`roost add endpoint` 按字段名把 typed Request 接到 Nest Sender；`roost add lifecycle` 生成实例级 Entity load/create/destroy 边界；`roost project doctor --workflow first-business` 检查整链并给出逐项修复命令。
- 玩家协议 Registry 在启动注册后 Seal 并预组合 middleware，以 atomic immutable snapshot 提供无锁 Dispatch/Encode 热路径；seal 后注册 fail-fast，避免线上动态接线与请求并发竞态。
- 新增 `roost add skill`，创建中性的 `cube.skill/v2` JSON 骨架与稳定 `/skill` 包的启动期 CompileAll catalog；重复 ID、解析错误和 error diagnostic 均 fail-closed。
- 生成项目新增 FIRST_BUSINESS、ENTITY_LIFECYCLE、PROTOCOL_TO_NEST、SKILL、TROUBLESHOOTING 五份分层文档，并在 Makefile 暴露 new-access/new-lifecycle/new-endpoint/new-skill。
- 新增零基础 Entity 聚合工作流：`roost add component <name> --entity <owner>` 自动生成同包 Component、类型安全工厂、Owner 访问器并接入 Entity；`roost add dao <name> --entity <owner>` 自动接入 import、DaoManager、字段 tag 和接口 getter。生成项目新增 `docs/ENTITY_COMPONENT.zh-CN.md`，CLI 新增 `roost help beginner` 和逐步 next 提示。
- 生成项目新增 `docs/QUICKSTART.zh-CN.md` 零基础教程与 `docs/ROOST_YAML.zh-CN.md` 字段级参考，覆盖全部顶层/嵌套字段、完整示例、Service/Mod/Feature 概念、轻量首次项目、常见错误和阅读路径。
- `project upgrade` 明确支持旧项目模板迁移，新增 `--dry-run` 预览，并允许先读取低于当前兼容下限的旧版本策略、合并新策略后再校验；生成 Makefile 新增 `project-upgrade`，通过 `roost-codegen@latest` 刷新受控文件，并让 core、kit、skill、codegen 持续跟随最新版本。旧生成 Makefile 会自动获得 `roost-up`、`codegen-up` 等当前目标，自定义 Makefile 则安全拒绝覆盖。
- 新项目 Makefile 新增 `roost-up`（`GOWORK=off go get -u ./...` + `go mod tidy`）和 `codegen-up`（安装 `roost@latest`）；与只更新三个框架模块的 `deps-update` 分工明确，并同步到内置 help 与项目文档。
- 安装后的 `roost help` 升级为能力目录，并新增 `roost help <capability>`、`roost help all` 和上下文 `--help`；28 个专题均提供用途、命令、配置/marker 和可复制示例，覆盖环境、新手路径、项目、全部生成器、版本、Saga、帧同步与部署。
- 项目生成器新增生产部署基线：Shell 静态构建与 systemd 版本化发布（不可覆盖 release、校验和、原子切换、readiness 失败自动回滚）、distroless 非 root Docker 镜像、Kubernetes Deployment/StatefulSet、独占 WAL PVC、Secret 配置挂载、健康探针、PDB、默认 NetworkPolicy 和安全上下文。
- 生成项目 README 分为新手快速使用、老手完整使用、框架实现与生产部署三级阅读路径。
- CI 新增部署 Shell 语法校验、Kubernetes YAML 解码、生产 Docker 镜像构建和发布版本生成项目 smoke test。
- golden 全量输出比较只规范化 Git checkout 的 CRLF/LF 差异，修复 Windows CI 因 `core.autocrlf` 产生的伪模板漂移；其余内容仍完整比较。
- 新增 `docs/CODEGEN_REFERENCE.zh-CN.md`，覆盖项目、统一流水线及全部独立生成器的参数、marker、输入输出、完整示例、CI 用法和常见错误。

## [1.6.0] - 2026-08

### Fixed（cfggen 对抗性复审 14 项，均带回归测试）
- **`index: false` 不再被当作"开索引"**（此前 `!= nil` 判定对任何非 null 值都成立——显式关掉的索引被打开）；`index` 只接受 true/false/名字，其他类型报错。
- **globals 上的 `ref`/`index` 直接拒绝**——此前生成的 cfg tag 在对象注册路径是死代码，用户以为有悬空引用兜底实际零校验。
- **统一的生成标识符注册表**：表/全局/bean 的类型名、访问器名（含索引访问器）与固定函数名全量查重——`my_table` vs `myTable`、表 `item` vs 全局 `item_table`、bean 撞行结构名等此前静默生成编译不过的代码而生成器报成功。
- 全部名字（表/全局/字段/index 名）强制 Go 标识符字符集（`my-table`、非 ASCII 名此前报成 "generator bug"）；bean 名额外拒绝关键字与预声明标识符遮蔽；bean 字段补空名/重名/PascalCase 碰撞检查；bean 沿非切片字段的递归拒绝（`[]Node` 合法保留）。
- 关键字/保留参数名防护：字段名 `type`/`range`/`table`/`snap` 等生成 `typeArg` 式参数（`type` 是配置表最常见字段名之一，此前直接生成语法错误）。
- 多行 comment 折叠为单行（此前 YAML 多行注释的后续行会落到生成文件顶层——可注入任意声明）；`file` 拒绝绝对路径与 `..`；`format.Source` 失败不再误报 "generator bug" 并附原始源码。

### Added
- `cfggen`：meta 字段新增 `required: true`（配合 ref，零值即错——抓数据侧字段改名导致整列静默归零）与 `skipempty: true`（配合 index，零值不进索引），生成对应的 `cfg` tag 指令；前置条件（required 需 ref、skipempty 需 index）生成期校验。meta 文件完整参考文档：`docs/CFGGEN_META.zh-CN.md`（结构/类型系统/命名映射/两层校验清单/CI 门禁建议/易错点）。
- `cfggen`：配置 schema 生成器（简化版 Luban）——一个 YAML meta 文件定义表/对象/bean（字段类型、key、index、ref），生成 cube-core/configdata 绑定：行 struct（`json` + `cfg` tag）、`RegisterGeneratedConfigData`、类型化 `XxxTableFrom`/`XxxFrom` 访问器，二级索引额外生成强类型查询函数（`MonsterBySceneID(snap, sceneID int32) []MonsterCfg`，字符串化规则与运行时索引一致）。全局单例配置段命名为 `globals`（`objects` 保留为兼容别名）。生成期校验：key 必须声明且为整数/字符串、ref 目标表存在且类型与其 key 一致、index 仅限字符串/整数/bool、meta 未知字段拒绝（KnownFields）。业务只写 meta 文件 + 一行注册。与 `tablegen`（Go 元数据先行）互补：`cfggen` 是 schema 文件先行。运行时依赖 cube-core 的 `RegisterAutoTable`（v1.7.1 起）。端到端示例见 cube-core `examples/configgen`。

## [1.5.2] - 2026-08

### Added
- CI 增加 `release-hygiene` 门禁（module 路径可解析 + tag 与 major 匹配）。

## [1.5.0] - 2026-08

破坏性变化：dao 生成器由"静默容错"全面改为 fail-fast，升级前请阅读 README 的 v1.4→v1.5 迁移章节。

- 孤儿标记（标记与 struct 之间隔注释/被 gofmt 移位）不再静默丢失，直接报错。
- 字段 `dao` tag 必须声明意图：空 tag、未知选项、未知 `map=` 值全部编译期报错并给出改法（此前会静默把 persist/sync 归零）。
- 分组 `type (...)` 声明上的单个标记不再静默绑定组内每个 struct（数据损坏级回归，已修并带回归测试）。
- 生成物写路径不再吞错：`bson.Marshal` 失败 panic 而非静默丢数据（含 nested 模板）。
- DAO 改名后的孤儿 gen 文件自动清理（带 DO-NOT-EDIT 头守卫；含删光定义的目录清扫）。
- golden 测试基建（`internal/genutil`），golden 集覆盖 nopersist/nosync、map=fast/sharded、redis raw 等 7 类。
