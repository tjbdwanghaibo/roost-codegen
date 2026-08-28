# Changelog

本文件从 v1.5.0 起维护；更早版本见 git 历史。

## [Unreleased]

### Changed
- 生成代码统一导入稳定的 `github.com/tjbdwanghaibo/roost-skill/skill`，删除 `/skillv2` 路径；相应把 roost-skill 兼容下限提高到首个提供稳定包的 v1.9.0。
- 项目版本策略默认改为 core、kit、skill、codegen 全部跟随 `latest`；明确版本只要不低于兼容下限即可。
- release-hygiene 增加根模块 `replace` 禁止门禁，防止本地联调路径进入发布 tag。
- 新项目 `go.mod` 直接 require 三个真实发布 module，不再生成 `v0.0.0 + replace`；`project new/sync/deps` 联合执行 core、kit、skill 的 `@latest` 解析并 `go mod tidy`，失败时恢复原 go.mod/go.sum。新增无运行时副作用的 `internal/frameworkdeps` 类型别名，保证尚未被业务使用的 skill 在 tidy 后仍保留为直接依赖。
- Entity generator 的 checkpoint、删除 tombstone 和 Remote Entity 路径统一改用 DAO `DirtyTracker()` 方法，不再访问已经私有化的 `Tracker` 字段；DAO 与 Entity 生成物恢复同代可编译。

### Added
- 生成项目新增 `docs/QUICKSTART.zh-CN.md` 零基础教程与 `docs/ROOST_YAML.zh-CN.md` 字段级参考，覆盖全部顶层/嵌套字段、完整示例、Service/Mod/Feature 概念、轻量首次项目、常见错误和阅读路径。
- `project upgrade` 明确支持旧项目模板迁移，新增 `--dry-run` 预览，并允许先读取低于当前兼容下限的旧版本策略、合并新策略后再校验；生成 Makefile 新增 `project-upgrade`，通过 `roost-codegen@latest` 刷新受控文件，并让 core、kit、skill、codegen 持续跟随最新版本。旧生成 Makefile 会自动获得 `roost-up`、`codegen-up` 等当前目标，自定义 Makefile 则安全拒绝覆盖。
- 新项目 Makefile 新增 `roost-up`（`GOWORK=off go get -u ./...` + `go mod tidy`）和 `codegen-up`（安装 `roost@latest`）；与只更新三个框架模块的 `deps-update` 分工明确，并同步到内置 help 与项目文档。
- 安装后的 `roost help` 升级为能力目录，并新增 `roost help <capability>`、`roost help all` 和上下文 `--help`；21 个专题均提供用途、命令、配置/marker 和可复制示例，覆盖项目、全部生成器、版本、Saga、帧同步与部署。
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
