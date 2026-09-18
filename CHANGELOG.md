# Changelog

本文件从 v1.5.0 起维护；更早版本见 git 历史。

## [Unreleased]

### Added

- **game-demo：送礼 saga 的两个 Nest 步骤改走原生路径**（§9.1 第 2 条，需 core ≥ v1.15.7）。debit 与它的补偿（新 handler `GiftRefund`，不再借用 `AddItem`）用 `saga.SubscribeDataEngineStep`：handler 在自己的 Nest 事务里 `inbox.Bind(command, reservation)` + `saga.EmitCompletion(...)`，背包变更、命令回执、协调器等的完成结果落进**同一条 WAL 记录**——重投撞上回执回放已存结果，提交前崩溃则三样都没发生。此前这是两次提交，中间的窗口写在注释里当边界。deliver 的业务是一次 bus 调用、没有事务可绑，**刻意留在** `SubscribeMongoStep`（第二层幂等是 mail 服务的 RequestID 去重）；这条分界是规则，拿原生路径包跨服务调用等于把回执绑在不含那次副作用的事务上。业务拒绝也提交（不动数据，只写回执和失败的完成结果），只有基础设施错误回滚重投。新增 `gift.NativeStep` carrier 与 `Complete(success, reason)`。实跑：6 机器人全过、6 completed + 6 compensated，Mongo 里 `saga-step` 回执 18 条（debit + refund）、Mongo step inbox 12 条（全是 deliver）。
- **`roost add saga` 生成步骤 topic 常量**（`TopicDebit` / `TopicDebitCompensation` …）。此前只生成绑定 Mongo inbox 的 `Subscribe*` 助手，想走原生路径就得自己重复 topic 字符串；现在 durable 与 filter 由同一份常量给出，不会漂。
- **game-demo：邮件附件的领取做成恰好一次**（§9.1 第 1 条，与 dungeon 清关奖励同形）。`ClaimMail` 的三步里，中间那一步换成 `ClaimMailReward` 事务：邮件 id 与道具进同一条 WAL 记录（`db/def/player.go` 的 `MailClaims` 账本、`Bag.ClaimMailReward`、新 errcode `mail_claim`(100011)）。三次调用不可能合成一个事务——邮件在另一个进程——所以窗口是真的：发放成功、`CommitClaim` 丢失、预留到期、重试用同一个 token 再预留一次；邮件服务只能算作重复**尝试**（它无从知道游戏发没发），游戏这边知道，第二次发放什么也不做。账本按时间清理，保留期长于 `mail.send_ttl`（720h），论证在 `game/rewards`。随工程生成 `game/handler/claim_mail_reward_test.go`：真实 handler 跑在真实 Nest 事务里（一个只发一个实体的 Getter + 记录型 committer + 工程自己的配置数据），去掉账本判断即红（重放拿到第二叠）。机器人加 `claim_mail_replay`。仍然开着的两处写在 README。

- **生成工程一条命令起全部服务：`make dev-run` / `dev-stop` / `dev-status` / `dev-smoke`**（`deploy/dev/run.sh`，codegen 受控）。按托管服务 → 业务服务的顺序 `go build` 后起每个进程、等各自 `/readyz`，有 `cmd/accountctl` 的工程（game-demo）顺手把 sid 注册进 account；pid 与日志在 `.dev/`（已入 .gitignore）。配套：**每个服务的本机配置有自己的 ops 端口**（业务服务按名从 9100 起，托管服务接在后面；生产配置仍统一 9100），此前五个进程都监听 9100、同机只能起一个。
- **game-demo：chat 服务进链路**。`internal/service/chat/collaborators.go` 给出写成决定的策略（world / private 开放、group 拒绝、system 只读）、唯一的 `text` 类型与授予的系统路径；`game/chatroom/` 是游戏侧契约（频道、文本校验、每进程 presence）；新协议 `SendChat`（10006）、`ChatHistory`（10007）、推送 `ChatMessage`（10101）；EnterGame 经 `PublishSystem` 在 world 频道公告登录并扇出；机器人脚本加 send_chat → wait_push → chat_history。观测配置补 account / chat 的抓取目标。
- **`-template game` / `game-demo` 托管第五个服务：session**（`frameworkCatalog` 加 `session`：`NewMod(Release(), Metrics())`，配置 `session.key_prefix / run_ttl / request_ttl`；collaborator `Release()` 默认拒绝）。demo 给出释放器实现与副本链路：`EnterDungeon`（10010，Enter 幂等、一个 owner 一个活 run）/ `FinishDungeon`（10011，Finish 后经 AddExp 两实体事务发 100 exp，够升一级再走一遍奖励邮件）；机器人 claim_mail 后 enter_dungeon → finish_dungeon。ops 端口 session 9105，观测配置补抓取目标。
- **cfggen 生成物过真实配置运行时的 CI 门**（`scripts/cfggen-golden-runtime.sh`，ci.yml Linux 一步，C13）：把 cfggen 当场生成的包放进临时模块，钉当前 roost-core pin，跑 `internal/cfggen/testdata/runtime/roundtrip_test.go`——主键表、二级索引、bean 切片、全局单例、`ref` 悬空拒绝且不动现役快照、`required` 缺失拒绝、热更后新快照发布而旧快照不变。此前 cfggen 的测试只比对文本与 gofmt，和 U-0224 之前的 dao 是同一个盲区：把生成的 json 名去掉下划线（编译通过、注册正常、数据读不出来）在旧测试下全绿，在这条门下四条全红。
- **《数据流：六条路径》**（`docs/DATA_FLOW.zh-CN.md`，D15）：按路径而不是按功能组织——同步写（Nest → Data Engine）、事件链（outbox → 消费者）、跨服务 bus 调用、saga、帧同步、配置快照，每条给出谁保证原子性、幂等键是什么、失败后谁重试；外加"一次请求经过的边界清单"（过了就不能回头的动作与失败表现）与排错入口。生成工程 README 与 demo README 都链到它。
- **game-demo：实时战斗——lockstep 帧同步**（B10，`roost-core/lockstep` 的第一个使用方）。匹配成功后，形成比赛的 game 进程开一个 `lockstep.Room`（`internal/service/<game>/battle.go`：一条 goroutine 独占房间，端点投递命令而不直接调用），两名玩家打满 45 帧（30 Hz）。广播走 demo 已有的 player TCP 推送（`BattleFrame` 10102，包里带冗余帧），输入回来是普通请求（`BattleInput` 10015，一条消息同时带本帧输入、关键帧哈希、补发请求）；`game/battle` 是两端共享的契约（帧预算、输入编码、座位、确定性模拟）。房间切完最后一帧留 2 秒收尾窗口，否则最后一个关键帧的哈希报告必然被拒——而那正是 desync 裁决要的。机器人用 `roost-core/robot` 的 `LockstepBot` 跑真客户端：推送转交给动作自己的循环（在会话读循环上应用帧会把读循环堵死在自己的响应上），bot 按序应用帧并回送本座位的输入与哈希。实跑：6 机器人全过，每个应用满 45 帧，无 desync。p95 阈值 10s → 16s（成本直方图是 2 的幂桶，健康运行落在 8s 桶）。
- **game-demo：送礼 saga**（B9，`add saga` 的第一个真实使用方）。`SendGift`（10013）在发送方 Player 的 Nest 事务里检查背包并 `saga.EmitStart`（start 意图与事务同一条 WAL 记录，saga id = 发送方 + 会话 + 帧序号）；`internal/service/<game>/gift_saga.go` 四个 `SubscribeMongoStep` 步骤消费者：debit = `GiftDebit` Nest 事务（`Bag.RemoveItem`，新 errcode `item_short` 100006），补偿 = 既有 AddItem，deliver = 确认收件人进过游戏后 `mail.Send` 带附件（RequestID = IdempotencyKey）；`GiftStatus`（10014）经 `mods.ModSaga` 的 Engine 读记录；GM 加 `gm.saga.get` / `gm.saga.list`。机器人走完成路（送自己再领回）与补偿路（送给从未进游戏的玩家 1 → `compensated`），新增 `expect_gift` 自定义动作与 `send_gift_self`（`MapField` 取黑板 player_id）。边界写在文件头：Nest 提交与 inbox 回执不原子；原生路径的完成效果无人消费（core WANTED W-2026-09-17-04）。**补偿路需要 core ≥ v1.15.6**（U-0225，已随本组发版）。
- **`add mod` / `add saga` 给已生成的配置补上新 Mod 的段**。配置在项目创建时渲染一次、此后应用自有，后加的 Mod 只能靠代码默认值——game-demo 的 saga 因此用默认 8 GiB 建流、隔离环境起不来，而配置里没有 saga 段可改。现在按缺失的顶层键把 `modCatalog` 的段追加到 `config.<svc>.yaml` 与 `.prod.example.yaml`（生产示例经同一套 `productionizeConfig` 变换），已有的键不动、重复添加无变化（`add_mod_config_promises_test.go`）。
- **game-demo：GM 运维面**（B6）。`internal/service/game/gm.go` 在 app 的 admin 命令表注册 `gm.player.add_item` / `gm.player.add_exp` / `gm.mail.send`（可带奖励附件，`trace_id` 幂等）/ `gm.world.stats`，ops 经 `/admin/commands` `/admin/execute` 端出、token 鉴权；开发配置开 admin（`dev-gm-token`），生产示例关。`player_id` 收唯一 id 或 Mongo `_id`（完整实体 id）两种形式，响应同时给出两者——实跑时把 `_id` 当唯一 id 再包一层得到的是不存在的实体，现在按 `MatchEntityID` 识别。实跑验证：加道具落库、加 300 exp 升 2 → 5 级并计入 World 计数、带附件邮件 `trace_id` 幂等、无 token 401、坏载荷按 `command invalid` 拒绝。
- **game-demo：技能目录**（B7）。`roost add skill Fireball` + 写好契约的 `game/skills/fireball.json`；game 服务 `Init` 用 roost-core/skill 编译整份目录 fail-fast 并记 warning 数；`SkillCatalog` 协议（10012）列出编译出的 id 与 warning 数；机器人断言。技能执行刻意不进 demo。
- **`project next` 的进阶引导**（D16）：必做链完成后列出框架有、工程没用的能力——`add rpc`、`add saga`、attribute、`add skill`、webroute、cfggen——每条一个命令一个理由（`optional:` / `why:`），用了的不再提示。
- **换行不是内容**（C14）：生成工程带 `.gitattributes`（生成的文本文件钉 LF）；`generate --check` 与 `servicerpc -check` 比对前把 CRLF 规范成 LF——Windows 检出（core.autocrlf）不再被当成 stale（review 09-16 第五轮观察项）。
- **`roost add rpc <Name> -service <owner>`：本工程自己的跨进程服务一条命令到装配**。`internal/rpc/<name>/`：`//roost:rpc` 接口（`ErrRequestInvalid` 的码从 `ids.errcode` 分配、`Error` 映射）、实现骨架、owner Mod（发布 capability，Start 在 bus 注册 handler）、独立进程用的 Server 钩子，servicerpc 当场生成两半。清单新增 `services.<name>.rpcs` / `uses_rpcs`（校验：一个拥有者、不能自用、托管服务不能拥有 / 使用），feature `rpc` 自动启用，`make generate` 的 servicerpc 步骤逐包重生成；bootstrap 给拥有者装 `NewMod(New())`、给调用者装 `NewClientMod()`，两边自动带 nats。`make new-rpc`；framework-compat 的 full 场景加了 `add rpc Guild` + gate `uses_rpcs`。
- **framework-compat 的 demo 场景现在真跑**：compose 起基础设施 → 生成的 `deploy/dev/run.sh start` 起六个进程 → `go run ./cmd/loadtest -count 6` 走完整条链 → stop；失败时倾倒 `.dev/*.log`。以前那个 cell 只编译。
- **dao golden 过真实编解码器的 CI 门**（`scripts/dao-golden-runtime.sh`，ci.yml Linux 一步）：把 dao 生成器的 golden 放进一个临时模块，钉当前 roost-core pin 与 mongo-driver v2，跑 `internal/dao/testdata/runtime/roundtrip_test.go`（提交文档、回滚快照往返，DirtyHook 不入文档，nil 指针元素保留 null）。codegen 自己的测试只比对文本——U-0224 就是这样漏的。
- **game-demo：ranked 队列**（`ScoreWindowGrouping` 的第一个使用方）。`game/matchmaking` 变成 `Pools()`：duel 按到达顺序、ranked 按等级（窗口 5，每秒放宽 5，上限 50）；`PlayerLevel` 读 handler 在 Player 锁内取等级作 ticket 的 Score；`JoinQueue` / `PollMatch` 按 `mode` 选队列（未知 mode 是 coded 拒绝）；`sweep_queues` 列出两个队列；机器人 duel 之后再排一次 ranked。
- **game-demo：mail 服务的客户端一半**。升级奖励邮件带附件（`game/rewards/`：一个道具一叠，发件方与领取方共用编码）；新协议 `ListMail`（10008，含 `Claimable`）与 `ClaimMail`（10009：ReserveClaim → Sync_AddItem → CommitClaim，失败 CancelClaim）；机器人 add_exp 后轮询邮箱并领取，断言背包计数。README 写明 demo 未把 claim token 带进 Nest 事务的边界。
- **game-demo 一次 `doctor` 全绿**：account 的演示 Verifier 报错文案含"is not configured"，被 doctor 的 collaborators 桩标记误判为未实现；改写文案。

### Changed

- **`add saga` 生成的订阅助手改调 `saga.SubscribeMongoStep`**（`SubscribeStep` 已标 Deprecated，行为相同）。
- **game-demo 机器人 p95 阈值默认 5s → 10s**：场景现在等四条异步链（奖励邮件、两个送礼 saga、匹配），成本落在 4s 桶边，下一个桶是 8s。

### Fixed

- **attribute 的 `runtime.go` 现在带正确的生成标记**（U-0235，C4；自查发现，T-129）。U-0230 的模板自造了一句 `// Code generated by roost. DO NOT EDIT.`，而所有权判断认的是 `generatedHeader`（`roost-codegen`），于是启用该 feature 的工程 `project doctor` 的 project-templates 整项 FAIL、`project sync` / `upgrade` 也拒绝回写这个本该受控的文件。新 feature 的验收只跑了编译与运行时门，没对启用它的工程跑一次 doctor，demo 当时也还不带 attribute。测试 `internal/roost/attribute_runtime_promises_test.go`（标记 + 生成工程跑 doctor 的同一条检查）。**用 v1.15.10 生成过的工程**要先删掉该文件再 `project sync`。记录 `roost-core/docs/bugfix/U-0235-attribute-runtime-header.md`。

- **dao：嵌套结构里更深一层的变更会标脏父级了**（U-0232，C2；RR-20260917-05，Wanted-02 转入，T-126）。DAO 会把自己的嵌套字段接到 `markXDirty`，但一个嵌套结构**内部**的嵌套（`EquipInfo` 的 `gems map[int32]*GemInfo`）三个装载入口——公开 setter、raw 恢复、BSON 恢复——都只放值不接线。于是 `equip.GetGems(1).SetLevel(99)` 改了 child，父级不脏，DAO 顶层拿不到补丁：内存里变了，落库时不在 patch 里，回滚快照与同步同样看不见。现在三处统一 `val.SetNotify(s.Mark)`，并在 `SetX` 替换时把旧 child `SetNotify(nil)` 解绑——一个已经脱离的 child 还在标记它不再属于的父级，是没人能解释的脏标记。slice 与单个 struct 字段同形处理。U-0224 修的是"嵌套值有没有 BSON 表示"，与本条互不否定。测试在 dao 运行时门 `testdata/runtime/roundtrip_test.go`（三个入口各一条 + 替换解绑）。记录 `roost-core/docs/bugfix/RR-20260917-05.md`。
- **game-demo：FinishDungeon 的重放、failed、expired 不再发经验**（U-0226，C2；RR-20260917-08，P1，T-120）。端点按请求里的 `Success` 判定，而 `session.Finish` 对终态 run 是幂等返回（不报错、不改结束时间）、过期 run 无论调用方要什么都变 expired——模板注释里"重试会被 ErrRunTerminal 拒绝"从来不成立。现在只有服务返回的 `run.State == StateSucceeded` 才进发奖，且发奖走新的 `ClaimDungeon` 两实体事务：run id 写进 Player 的 `DungeonClaims` 账本，与经验、World 计数同一条 WAL 记录，只有"本次事务写进去的"才发，重放答 `ExpGranted=0`。账本按时间清理（保留 4h > `session.run_ttl` 30m，超过就再也不可能被重放命中），清理在写入口。新增 `game/dungeon` 契约与随工程生成的表驱动测试、errcode `dungeon_run`(100010)、机器人动作 `finish_dungeon_replay`。实跑：旧判据下机器人红（`replayed finish was paid again: exp=100 levels=1`），修后 6 机器人全过、Mongo 里有 `dungeon_claims`。记录 `roost-core/docs/bugfix/RR-20260917-08.md`。
- **attribute feature 真的可用了，game-demo 也用上了它**（U-0230，C4；RR-20260917-06，Wanted-03 转入，T-124）。生成物引用 `AttrID` / `AttrValue` / `AttributeMeta` / `AttributeProfile` / `Snapshot` / `Container` / `Selector` 七个类型，而三仓都不提供、脚手架只产出空包——空 feature 能编译，真写 profile 就整包编译不过。框架半落到 **roost-core 新增的 `attribute` 包**（≥ v1.15.7）；生成器改成产出包级访问器 `<Name>Of(Snapshot)` / `<Name>In(*Container, Selector)` / `<Name>Live(*Container, Selector)`，不再给 `Snapshot` / `Container` 挂方法——Go 不允许给外包类型定义方法，这正是"只加 alias"这条捷径走不通的原因；启用该 feature 的工程因此拿到 codegen 受控的 `game/gameplay/attribute/runtime.go`（一组别名 + `Base` / `Final` / `NewContainer`）。`dirtyMask uint64` 约定显式化：profile 结构体漏了它在**生成期**报错。新增运行时门 `scripts/attribute-runtime.sh`（ci.yml 一步，core 未带该包时自跳过）：脚手架 runtime + 真实 profile + 生成物进临时模块，跑类型化 setter、dirty 位、派生公式、元数据、导出/载入往返与快照隔离。**game-demo 随之补上 B8**：`game/gameplay/attribute/combat.go` 三个属性、一个派生公式、`LevelUp` / `ForLevel`，随工程生成 `combat_test.go`。记录 `roost-core/docs/bugfix/RR-20260917-06.md`。
- **`sync=true` 的实体生成物现在能编译**（U-0229，C4；RR-20260918-01，T-123）。生成的 wire 文件写了 Core 没有的 `FlushPolicy` 字段、`entity.SyncFlushOnEntityRelease` 常量和 `SubjectPackerFactory` 字段——`entity.EntitySyncBuilderParam` 只有 `Enabled` / `Topic` / `PackerFactory`，所以任何带 `sync=true` 的工程都编译不过。本包只比对文本、golden 里写着同样的过时字段、demo 又不用 sync，于是整条 feature 没有使用方。现在只写 Core 真有的三个字段；`subjectPacker` 是现行标记、`syncPacker` 是它的别名（老工程不必改），**同时写两个当场报错**（一个字段两个值），packer 没配 `sync=true` 也报错。新增运行时门 `scripts/entity-sync-runtime.sh`（ci.yml Linux 一步）：把当场生成的实体放进临时模块、钉当前 core pin，编译并断言 `BuildEntity` 真的建立了可用的 Sync 状态，外加 `sync=false` 对照。三条编码旧契约的老测试改成断言新不变量。记录 `roost-core/docs/bugfix/RR-20260918-01.md`。
- **game-demo：战斗房间的启动宽限期真的会开帧**（U-0228，C2；RR-20260917-09，P3，T-122）。常量注释约定"等首次输入、最多宽限 2 秒后开始"，但 `started` 只有收到 command 才置真，宽限期只参与那个合并的超时计算——没人输入的房间一帧都不切。现在 `grace` 与 `lifetime` 是两个独立计时器：宽限到点开帧（缺席的座位贡献空帧，迟到的从历史补发追上），`battleMaxLifetime` 只当绝对兜底。顺带把房间的推送侧收成 `battlePusher` 接口、房间加帧计数，于是工程自带 `internal/service/game/battle_test.go`：无输入时宽限后必须开帧且每个座位都收到帧、房间必须在上限内自行结束。记录 `roost-core/docs/bugfix/RR-20260917-09.md`。
- **nest：handler 半不再 import 只被返回类型用到的包**（U-0227，C8；实施 RR-20260917-08 时发现，T-121）。返回类型来自第三个包（既非实体参数也非普通参数）的 handler，生成出的 `<x>_nest_gen.go` 会 import 它却从不引用，整包 `imported and not used`。handler 半只有 `ret any`，真正写出返回类型名的是 sync sender，所以收集条件改成只对 sync sender 生效；既有 handler 没暴露是因为返回类型要么内置、要么来自实体参数已 import 的包。测试 `internal/nest/return_type_imports_promises_test.go` 用 go/parser 查未引用的 import（`format.Source` 不做这件事）。记录 `roost-core/docs/bugfix/U-0227-nest-return-type-imports.md`。
- **dao：生成的嵌套 struct 现在有 BSON 表示，`DirtyHook` 不再进文档**（U-0224，C2；用户复审提出，T-118）。此前嵌套 struct 字段全部未导出且没有 `MarshalBSON`，父 DAO 把它放进 `bson.M` 后反射编码只看见导出的内嵌 `DirtyHook`，Mongo 里落的是 `{"dirtyhook": {}}`——嵌套数据没存、回滚快照与同步同丢。现在内嵌打 `bson:"-" json:"-"`，每个嵌套类型生成**wire 形式** `<name>BSONDoc`（导出字段、snake_case 键）与 `bsonDoc()` / `setBSONDoc()` / `xFromBSONDoc` / `xPtrBSONDoc` 转换；父 DAO 的落库 / 回滚快照 / 补丁 / 同步文档直接放 wire 形式让反射内联编码，不经过 `bson.Marshaler`（那条路每个嵌套值多一次 `[]byte` 与装箱：同一份 HeroDao 文档 82 → 63 allocs、4.9 → 3.3 µs，解码 112 → 93 allocs）；`MarshalBSON` / `UnmarshalBSON` 仍保留给单独编码的嵌套值。每个有嵌套的包多一份 `gen_dao_bson_helpers.go`（泛型 `daoMapDocs` / `daoSliceDocs`）。**重生成** `gen_*_nested.go` 与 `gen_*_dao.go` 即生效；历史文档里的嵌套字段本来就是空的，无迁移。测试 `nested_bson_promises_test.go`；真实驱动往返见记录 `roost-core/docs/bugfix/U-0224-dao-nested-bson.md`。
- **game-demo 机器人 transport 把服务端推送当成响应**（`loadtest/playertcp/conn.go`）。服务端给推送编的是自己的会话序号，与客户端 wire 序号同起点 1，一条世界频道推送恰好带着某个在途请求的号就被当作它的响应（`response msg mismatch: got 10101 want 10000`）。现在按帧头的 server-push 标志位分类，推送一律 Seq 0。此前只有 MatchFound 一种推送、且只在 wait_push 期间到达，所以没暴露。

- **`servicerpc -emit transport|assembly|all`、`-out <dir>`，`-dir` 接受 import path**（M-11，ARCH-01 / ARCH-04 收尾）。接口住在 roost-core 领域包时，core 包跑 `-emit transport`（生成物只依赖 core），kit 包跑 `-emit assembly -dir github.com/tjbdwanghaibo/roost-core/service/<x> -out .`（在 `-out` 的模块上下文里用 `go list` 解析 import path，不用写模块缓存路径）。文件头的"Regenerate with"记录实际命令。`GenerateWith(service, Options{Half, Regenerate})`；`-check` 对 `-out` 目录判定。测试：`halves_promises_test.go`。kit 的 mail / session / match 已按此生成。

### Changed

- **`servicerpc` 生成的传输拆成两个文件**（M-10，ARCH-04 生成器部分；来源 `roost-core/docs/bug/REVIEW-2026-09-16-04.md` §7）。`<接口名小写>_rpc_gen.go` 现在只含常量、wire 类型、handler 表、`BusClient`、capability 包装与 `CapabilityName` / `LocalCapabilityName`，只 import roost-core；`Server`、`OwnerCapabilities`、`ClientMod` 移到新文件 `<接口名小写>_rpc_assembly_gen.go`，它是唯一 import `roost-kit/mods` 的生成文件。两个文件落在同一个包，调用方不用改；拆分是为了让 RPC 接口能连同 wire / handler / BusClient 一起搬进 core 的领域包（M-06～M-08 的下一步）。`-check` 对两个文件分别判定，老仓库第一次跑会报装配文件 `(missing)`——跑一次 `go generate ./...` 即可。`Generate` 的签名从 `([]byte, error)` 变为 `([]servicerpc.File, error)`（内部包）。测试：`split_promises_test.go`；golden 拆成 `shop_rpc_gen.go.txt` + `shop_rpc_assembly_gen.go.txt`。记录：`roost-core/docs/bugfix/M-10-servicerpc-split.md`。

### Fixed

- **托管服务的 collaborators 文件不再无条件 import 服务包**（U-0218，C4，发版验证发现，codegen v1.15.6 补丁）。U-0217 删掉 match 的 `Grouping()` 之后正文只剩 `Metrics()`，`renderFrameworkCollaborators` 仍写入 `"roost-kit/service/match"`，生成的 `internal/service/match/collaborators.go` "imported and not used"，**整个工程编译不过**；codegen 自己的测试不编译生成物，v1.15.5 带着它发了出去。现在按 AST 判断正文是否有 `<pkg>.` 选择表达式再决定是否 import（注释里的 `match.Grouping` 不算）。`collaborators_imports_promises_test.go` 对目录里每个托管服务断言"每个 import 都被引用"，修前 match 红。用 v1.15.5 生成过工程的：删掉那一行 import 即可，文件是业务所有不会回写。
- **framework-compat 的 demo scenario 现在也排 `released`**：生成的 game-demo 工程对 core v1.15.3 / kit v1.14.4 已能不带 go.work 编译、vet、测试与 `generate --check`，只剩 `minimum` 仍排除。

- **`roost add endpoint` 生成的端点把 `context.PlayerID` 直接当实体 id 交给 Sender**。Nest 寻址的是完整实体 id（unique id + kind
  + 锁档），`PlayerID` 只是 unique id，第一条真实请求就在 Nest 里死于 `entity id: invalid: kind N category is not registered`；
  脚手架能编译，此前没有任何一步真的把它跑起来——是 game-demo 的机器人压测第一次实跑发现的。端点现在解析 Nest handler 第一个形参的
  `<pkg>.I<Component>Entity`，从 handler 文件的 import 找到实体包，生成 `entity.BuildEntityID(context.PlayerID, <pkg>.EntityKind<Name>)`
  再调 Sender（与 `add lifecycle` 的写法一致）。`TestExplicitFirstBusinessWorkflowGeneratesAccessLifecycleAndEndpoint` 钉住修前红。
  已有工程的端点文件是业务所有、不会被回写，需按此改一行；未改的工程症状即上面那条错误（见 roost-core TROUBLESHOOTING）。

### Changed

- **发版清单升到 core v1.15.3 / kit v1.14.4**（codegen v1.15.5）；`source-head-check.sh` 默认 pin 同步。core 带 U-0209～U-0216 与 M-06（`service/match`、`servicemetrics` 下沉），kit 带 U-0217（**破坏性** `match.NewMod(reporter)`）与别名包；生成工程的 `svcmatch.` 引用经别名不变，`framework-compat` 的 demo scenario 仍只排 source-head（实体 category 接线仍依赖 core main 之后的改动）。

- **match 服务不再生成 `Grouping()` collaborator，bootstrap 生成 `svcmatch.NewMod(serviceMatch.Metrics())`**（U-0217，RR-20260916-05，随 kit 的破坏性改动同版本升级）。kit 的 match Mod 曾接受一个从不执行的匹配策略参数，生成的 collaborators 注释还让项目"在这里替换成自己的规则"——一个静默失效的教学入口。现在 collaborators 里是一段说明：成组由游戏进程的 matchmaker 驱动（Candidates → `match.Grouping` → Commit），指向 game-demo 的 `internal/service/<game>/matchmaker.go`。已有工程 `internal/service/match/collaborators.go` 是业务所有、不回写，其中的 `Grouping()` 留作无引用函数即可；`project sync` 会重写 bootstrap 的 `NewMod` 调用。

- **生成的 player TCP 传输层新增 `RegistryBound` 钩子**：authenticator 若实现 `BindRegistry(*app.Registry) error`，Mod 在
  `Provide` 里（listener 启动之前）把进程的 registry 交给它，返回错误则进程不启动。此前 authenticator 只在 `Init` 拿到 viper
  配置，拿不到任何进程内能力——要用 account 客户端校验会话票据就没有入口。`server_gen.go` 是生成物，`project sync` 即得到；
  未实现该接口的 authenticator 不受影响。
- **`category=` 进实体标记并直接进生成物;生成的聚合注册末尾校验 entity 注册表**(M-05;前置 M-01～M-04)。生成的接线原来写 `entity.MustEntityCategoryOfKind(kind)`,一次运行期查表、查不到就 panic,于是"业务文件里手写的 `MustRegisterEntityKindCategory` 必须先跑"成了隐式前置,而这个顺序只由手写聚合文件的第一行保证。category 是 kind 的静态事实,标记里写清楚就能直接生成,前置随之消失。取值要求是导出标识符可带包限定,`category=1` / `category="player"` 这类写法在生成器就报错。没写 `category=` 时仍生成运行期查表,未迁移的工程不受影响。
  `roost add entity` 的实体文件不再手写注册,category 写在标记上;`roost add lifecycle` 生成的两处 `<pkg>.EntityCategory<Name>` 引用(M-04 删掉了那个常量,会让工程编译不过)改为 `entity.MustEntityCategoryOfKind(<pkg>.EntityKind<Name>)`。`registry.RegisterAll()` 是工程里唯一知道"注册结束了"的时点,聚合末尾因此调 `entity.ValidateEntityRegistry()` 并包装其错误,一次列出所有不一致;模板的 `fmt` 与 `roost-core/entity` 两个 import 变成无条件。实施记录见 roost-core `docs/bugfix/M-05-marker-owns-the-category.md`。
- **`remote=capable` 与 `remote=true` 系列拼写改为报错;`roost add entity` 脚手架归 `entity.EntityCategoryOther`**(M-04,**破坏性**,须与 roost-core 同版本升级)。core 删除了 `entity.RemotePolicyCapable`:它的全部作用是把 kind 放进第一个锁档,而锁档现在就是 kind 的 category。标记不再静默降级成 `none` —— 降级会改变一个已有实体的锁档 —— 而是报错并给出替代方案(把 kind 注册进某个 category,`remote=` 用 none / managed / mirror);`remote=bogus` 这类拼写错误仍得到原来的"不是这几个之一"信息。
  脚手架不再自铸 per-entity 的 `EntityCategory<Name> = 1`:那是留给远程托管实体的档,每个实体各铸一个会让它们全部排在所有东西之前且彼此同档、互相不能叠锁。生成的实体注册进 `entity.EntityCategoryOther`(安全默认:持有它之后什么都锁不了),文件里提示等顺序明确后再挪到 World / PlayerScoped / Player。`remote_capable_removed_promises_test.go` 与 `add_entity_category_promises_test.go` 修前红。消费方升级须知见 roost-core `docs/bugfix/M-04-drop-capable-and-the-group-hook.md`。
- **发版清单升到 core v1.15.2 / kit v1.14.3**（codegen v1.15.4）；`source-head-check.sh` 默认 pin 同步。
- **`rollbackSync` 的"回滚前文件不可检查"分支钉住**（U-0152，C2）。路径成了目录时报 "inspect ... before rollback" 而不是当作不存在去重建。`add.go:299`（同步失败且回滚也失败）只在 SyncProject 执行期间清单被并发改写或 I/O 故障时可达，外部无法构造，记为 rollbackSync 错误的防御性包装。
- **nil / 参数守卫收尾（codegen 八个包）：internal/dao、servicerpc、eventgen、tablegen、attribute、webroute、project、cfggen**（U-0151，C2）。各一条 `*_promises_test.go`，共 13 条守卫回退全红：redisdao 标记的四种畸形与 dao 标签 sync / nosync 冲突、接口重复方法与派生亲和键形式、位置参数点名拒绝、正整数解析、处理函数第二返回值必须是 error、目录多包拒绝、bean 字段全被目标组排除拒绝。
  `internal/roost/add.go:57 / 73` 由 `add_promises_test.go` 按文本覆盖、守卫失效后同步阶段报同一文本（冗余）；`add.go:299`（同步失败且回滚也失败）留待。
- **发布清单与本地 source-head 默认 pin 升到 core v1.15.1 / kit v1.14.2**（core：流水线提交落盘即唤醒投影 T-49；kit：mail 单读 / 批读同判 T-48）；codegen v1.15.3。
- **发布清单与本地 source-head 默认 pin 升到 kit v1.14.1**（activity sweep 组来源与后台循环失败计数随 kit v1.14.1 发布）；codegen v1.15.2。
- **生成器默认路径改为导出常量，`roost generate` 与工程模板引用常量而不再各写一份字面量**（U-0118，C4，classscan 观察 O-1）。
  `dao.DefaultDefDir / DefaultOutDir`、`eventgen.DefaultDefDir / DefaultOutDir`、`errcode.DefaultOutFile`、`protocol.DefaultDefDir / DefaultBindDir / DefaultHandlerDir / PlayerAgentImportSuffix / PBImportSuffix`、`tablegen.DefaultMetaDir`；
  值不变，生成物不变。`internal/roost/literal_coupling_test.go` 用 AST 扫描编排层的字面量，与任一常量相等即红（修前 16 处红）。
- **升级器不再带符号改名表**：`consolidation_imports.yaml` 只映射包路径，删掉 `renames` 段与改写器里的符号级重定向（`nats.Permanent` 回指契约包、`syncstream.HealthOptions` → `PublisherHealthOptions` 等）。
  `--consolidate` 之后这几个符号由编译器指出，改法见 roost-core TROUBLESHOOTING T-45；映射表不再需要随每次拆包维护符号条目。
- **发布清单与本地 source-head 默认 pin 升到 core v1.15.0 / kit v1.14.0**（P3b 装配下沉 + B-14 修复随 core v1.15.0 发布）。收敛边界（`minimumVersions` core v1.14.0 / kit v1.13.0）不变，`--consolidate` 与 minimum lane 不受影响。

### Added

- **`roost project new … -template game-demo`：一条能跑起来的写入链路，源码放在 `demo/` 目录里**。`game` 模板给的是骨架，
  新人拿到之后仍要自己想"实体、组件、DAO、Nest 事务、协议、端点怎么串"。`game-demo` 在 `game` 之上按真实顺序跑一遍
  `roost add`（component Profile / Bag → dao Player → handler AddItem → access player → transport tcp → protocol AddItem →
  endpoint AddItem），并把六个业务文件的内容一起写进去：`db/def/player.go`（含 `map=fast` 的 `Items`）、两个组件的业务方法、
  `game/handler/add_item.go`、`protocol/def/add_item.go`、以及一个 demo 认证器。生成出来的工程直接有
  "TCP 登录 → AddItem 端点 → Nest 锁 Player → 生成的 map mutator → dataengine 落库"这一条完整链路。
  步骤顺序本身是契约的一部分：`add endpoint` 会比对 handler 形参与协议字段，两边的文件必须都先落盘，
  这一点写在 `demoScaffoldSteps` 的注释里。
  六个文件以 `.tmpl` 后缀存放在仓库根的 `demo/` 下、路径与生成后一致，`go:embed` 进来、只替换 `{{MODULE}}`。
  它们 import roost-core，而 codegen 对运行时零依赖，所以不在 codegen 里编译；正确性由 CI 的
  `framework-compat` 新增 `demo` scenario 保证（生成工程 → build / vet / test）。该 scenario 只排在 `source-head` 上：
  codegen HEAD 生成的实体接线用 M-04 之后的 entity category，已发布的 core v1.15.2 里还没有，
  因此 `demo × minimum` 与 `demo × released` 被 exclude。
  写进工程的全部是业务文件（无 `Code generated` 头），`project sync` / `project upgrade` 不会回写。
  生成的 `internal/access/player/tcp/auth.go` 认 `player:<id>` 字符串，**不是认证**，文件头与 `demo/README.md` 都写明上线前必须替换。
  第二批：**item 配置表 + 错误码边界 + 可用的 account collaborators**。`roost add table Item` 加 `configs/schema/item.go`
  与 `configs/table/item.csv`（CSV 前四行为列名 / 标题 / 类型 / 规则），最终一次 `generate` 生成类型化 loader 并转出
  `configs/data/item.json`；`BagComponent.AddItem` 经 `generated.ItemByID` 校验道具存在与 `MaxStack`，失败返回
  `internal/errors` 里 `errcode.Define` 的三个码（100001–100003，`roost add errcode … -id` 落在清单号段里）；handler 改为
  `(int32, error)`，新数量经生成的 Sender 回到端点；端点文件由 demo 覆盖，`errcode.ClientError(err)` 把 coded error 换成响应里的
  `Code` / `Reason`——生成的 TCP server 把端点返回的 error 当坏帧断连，所以业务失败必须在这里换形状。
  `internal/service/account/collaborators.go` 换成能跑的版本：`Verifier` 只认 `demo` 渠道的 `demo:<open_id>` 凭据（不是身份校验，
  文件头写明）；`Allocator` 用 account 服务自己 Redis 里的 `INCR` 计数器，经 roost-kit 新增的 `account.RegistryBound` 在 `Provide`
  里拿到 registry。生成的工程 `go build` / `go vet` / `go test ./...` / `roost id check` / `roost generate --check` 全绿。
  第三批：**会话票据校验 + 升级事件链（事务性 outbox → 奖励邮件）**。auth.go 认 `session:<player_id>:<token>`，经 account
  客户端 `ValidateSession` 校验、principal 取 account 返回的角色；`player:<id>` 保留为终端调试捷径并标明不是认证。
  `AddExp` 加第二条 handler / 协议（msg 10001）/ 端点；`ProfileComponent.AddExp` 升级时 `effects.EmitPlayerLevelUp` →
  `nest.Emit`，effect 与状态变更进同一条 WAL 记录、回滚一起消失；`internal/service/<game>/level_up_mail.go` 用
  `nestwal.SubscribeJetStreamEffects`（durable + Mongo inbox）消费并 `mail.Send`，`RequestID = EffectID` 作第二层幂等；
  `service.go` 在 Init 订阅、Shutdown drain。demo 的 `internal/service/game/` 下文件按实际游戏服务名落盘
  （`{{GAME_SERVICE}}` / `{{GAME_SERVICE_PKG}}` 占位符），`TestDemoTemplateFollowsTheGameServiceName` 钉住。错误码增至 100005。
  脚手架多一类"运行"步骤：`player_access.tcp.enabled` 置 true（等价 `roost config enable player-tcp`），
  `roost project doctor -workflow player-tcp` 在刚生成的工程上全绿，测试直接调 `checkPlayerTCPWorkflow` 钉住。
  第四批：**机器人压测 = 回归测试**。`cmd/loadtest`（`go run ./cmd/loadtest -endpoint … -count N`）用 `roost-core/robot`
  的 runner / 场景树 / `loadtest.Manager` 阈值门跑 `loadtest/scenarios/demo.yaml`：connect → enter_game → add_item → add_exp，
  任一步非零 code 或 `error_rate` / `p95` 超阈值即失败并打印 JSON 报告。新增 `loadtest/playertcp`：生成的 player TCP 帧的
  robot 传输适配器（16 字节大端帧 + 握手 + 严格递增序号 ↔ core robot 的 12 字节小端帧）。新增 `EnterGame` 协议（msg 10002）
  与手写端点，控制器改为同时持有 `PlayerLifecycle`，`GetOrCreate` 补上"第一次登录没有 Player"这一环——此前 Nest handler 对
  不存在的实体会返回 ErrEntityNotFound。生成工程的 `cmd/loadtest` 随 CI 一起编译。
  本批首次在本地真实基础设施（隔离的 Mongo 副本集 / NATS JetStream 集群 / Redis）上跑通整条链：game 进程 + mail 进程，
  5 个机器人 connect → enter_game → add_item → add_exp 全部成功，Mongo 里 `game.player` 落库，进程重启后再跑一轮 items 1→2、
  level 1→3；升级 effect 经 JetStream 投递到消费者，mail 服务 Redis 里出现每个玩家的奖励邮件与以 EffectID 为键的幂等记录。
  实跑同时发现并修掉了上面 Fixed 里的端点 id 缺陷。
  第五批：**跨服务组队（game → match）+ World 的真实职责**。`game/matchmaking/queue.go` 定义 duel 队列与 player subject；
  `JoinQueue`（10003）/ `PollMatch`（10004）端点经生成的 typed match 客户端 `Enqueue` / `Ticket` / `Match`，帧序号作 Enqueue 幂等键；
  `internal/service/<game>/matchmaker.go` 在 game 进程里每 500ms `Candidates → Grouping.Group → Commit`，成组后
  `Sync_RecordMatch` 让 World 记一笔——对 match 的调用全部在实体锁之外。World 加 `Stats` 组件与 DAO（`PlayersEntered` /
  `MatchesFormed`），`RecordEnter`（EnterGame 之后的独立 Nest 调用）、`RecordMatch`、`WorldStats`（带 `world.Stats` 返回值的读
  handler）三个 handler，`WorldStats`（10005）端点读出计数。match 配置 `sweep_queues` 列出 duel 队列（run 步骤）。
  压测场景追加 join_queue → retry{wait 250ms; poll_match} → world_stats，`-count` 须为偶数。
  顺带记一个 kit 观察：match Mod 接受的 `Grouping` collaborator 在 store 里没有任何调用路径，成组完全由调用方驱动。
  实跑：game + mail + match 三进程，10 个机器人全部成功（error_rate 0，p95 1.0s，含刻意等待匹配的时间，默认阈值放到 2s），
  match 进程里成组 5 对，`db.world` 计数 players_entered / matches_formed 同步增长，每个升级玩家一封奖励邮件。
  过程中撞到一个环境陷阱并写进 `demo/README.md`：共享 JetStream 里残留的测试流（`ROOST_IT_RPC_REQ_*`）覆盖 `roost.rpc.>`，
  以 PubAck 抢答 RPC 请求，读调用大面积得到 `bus: unsupported rpc response version 0`；每次实跑给三个进程一个独立的 `nats.prefix` 即可。
  第六批：**可观测性**。`deploy/dev/observability/`：Prometheus + Grafana compose（与会被重生成的 `deploy/dev/docker-compose.yaml`
  分开）、抓取三个进程的 ops 端口与压测的 `-metrics-addr`、预置数据源与仪表盘 "Roost game-demo"（按链路分组：玩家接入 → Nest 分发与锁
  → WAL 落库 → 事件链与配置 → 跨服务 RPC → 机器人，18 个面板）、`README.md` 写清每个指标对应链路上的哪一步、该看什么。
  `cmd/loadtest -metrics-addr` 让压测进程也暴露 `/metrics`——机器人侧的直方图只在那里。仪表盘 JSON 合法性、查询非空、抓取配置覆盖
  由 `TestDemoTemplateGeneratesABuildableWritePath` 钉住；指标名对照真实进程的 `/metrics` 输出核过。
  交接文档写在 roost-core `docs/feature/GAME_DEMO_TEMPLATE.md`。
  第七批（交接文档 §7.1 / 7.2 / 7.4）：**`make loadtest`**——Makefile 模板加 `loadtest:` 目标（`LOADTEST_ENDPOINT / COUNT /
  METRICS_ADDR / ARGS` 变量，`test -d cmd/loadtest` 守卫让没有压测的 `game` 模板共用同一份 Makefile 并给出提示），`help-make` 同步。
  **真实登录**：`cmd/loadtest -account-nats` 在压测进程里起 bus，用 account 的 typed 客户端 `Login → CreateRole → SelectRole`，
  以 `session:<player_id>:<token>` 握手，每次运行用新 open id。`UpsertServer` 刻意不在 account 的 RPC 接口上（控制面写入，game 无权做），
  所以新增操作员工具 `cmd/accountctl upsert-server`：用 Redis 凭据直接打开 account 的 store 写服务器记录，环境准备时跑一次。
  第八批（交接文档 §7.3 / 7.7 + 收尾）：**实体锁档与两实体事务**——Player → `EntityCategoryPlayer`、World → `EntityCategoryWorld`
  （demo 覆盖两个 entity.go，注释写清档位即锁序、档位编进 id、改档等于迁移）；`AddExp` 改为
  `handlerAddExp(target player.IProfileEntity, stats world.IStatsEntity, amount)`，World 累计 `ExpGranted`，两处变更一条 WAL 记录，
  Sender 变为 `MultiSync_AddExp`，端点手写（`add endpoint` 只接单实体）。**删掉 `player:<id>` 调试凭据**：auth.go 只认会话票据，
  `cmd/loadtest -account-nats` 默认 `nats://127.0.0.1:4222`、`make loadtest` 传 `LOADTEST_ACCOUNT_NATS`。**`demo-publish.yml`**：
  main 上 demo / internal / cmd 有变更时，按 source-head 生成工程、build / vet / test / generate --check / id check 通过后
  force-push 到本仓 `demo-generated` 分支（带 GENERATED.md 说明来源 SHA），给人一个可直接 clone 来读的完整工程。
  第九批（交接文档 §7.6）：在本机 docker 里用发布的 compose 原样起 Prometheus + Grafana，数据源与仪表盘自动预置，33 条查询
  0 条 PromQL 错误、No data 的面板逐条可解释。仪表盘"请求速率 / 错误"面板加 `player_tcp_connection_rejected_total{reason}`：
  600 机器人的实跑正好 128 成功、472 被 `max_connections_per_ip`（默认 128）在握手前关掉——上限在工作而非缺陷，两处 README 写明。

  **成组推送**：`MatchFound`（10100，notify）协议，matchmaker Commit 后经传输层 `PushPlayer` 推给成员，场景改为 `wait_push`，
  `poll_match` 退为 `selector` 里的兜底。

- **生成物自带守卫测试**（U-0124）。远端托管实体的 `*_gen_wire.go` 旁生成 `*_gen_wire_test.go`，在业务工程里钉住生成代码内的三条远端提交守卫
  （无事务内持久化变更、DAO 级删除、别的实体的确认）；nest sender 包旁生成 `*_nest_gen_test.go`，钉住 nil 客户端 → `nest.ErrNestStopped`。
  这些守卫在模板字符串里，生成器自己的单测触不到（gap map 反复标 GREEN），只能在编译它们的工程里跑。未改动的一次普通 `roost generate` 也会补出配套测试；
  实体不再是远端托管或 DAO 全为 cold 时删除过期的配套文件。带 `Code generated` 头，`generate --check` 与工程清理按生成物处理。
- **`scripts/gapmap/classscan.py`**（与 roost-core 同一份拷贝）：C3 / C4 / C5 / C6 / C7 / C8 的启发式候选扫描；internal 十六包首轮扫过，无真洞，记两条观察（生成器默认路径与 `internal/roost` 重复字面量；`render.go` 依赖 `LoadManifest` 已校验）。
- **internal/protocol 与 internal/nest 的解析守卫钉住**（U-0115 / U-0116，C2）。nightly gap map 各 20 条采样 8 条无覆盖。
  protocol：`roost:msg` 的非数字 id、非命名结果类型、非 snake_case handler、通知引用不存在的结构体；`validateDefinitions` 的 req / resp id 不等、枚举重名、枚举无值；
  有控制器域而无 handler import base 时 bootstrap 拒绝。`guards_promises_test.go` 三条；回退 8 处守卫各红。
  nest：同一类型上的重复远端别名（同结构体两字段、包内两文件各一处）、向上找不到 go.mod、目录在模块根之外。`guards_promises_test.go` 两条；
  回退 8 处 3 红、5 处不红：`empty module path`（整行 TrimSpace 后 `module ` 前缀不可能剩空路径）、方法多接收者（Go 语法不允许）、"non-error return after error" 被前一条
  "error must be the single final return value" 前置、结构体级别名检查被文件级聚合检查前置，均不可达 / 冗余；模板字符串内的 `nestClient` 守卫是生成物，属"生成 + 编译 + 运行"那套基建。
- **gap map 采样器跳过 `*_gen.go`**（B-25）：生成文件是同一模板在每个包的实例，其守卫在模板所在处钉一次即可；采样器现在只统计不采样，并在包级与总计里报告跳过的守卫数。
- **cfggen 导出分组（前后端分开的配置）**：meta 顶层 `groups: {names: [c, s], target: [s]}`，表 / 全局 / 字段可写 `group: c` 或
  `group: [c, s]`；不在目标组里的字段从 struct 中去掉（连带索引访问器），不在目标组里的表 / 全局不生成、不注册、无访问器；`-groups c,s`
  覆盖 meta 的 target。语义与 Luban 的 `group` 属性一致：不写 = 属于所有组；没有 `groups` 的旧 meta 生成结果不变。生成期拒绝：未声明的组名
  （字段 / 条目 / target / 参数）、组重复声明、主键字段被排除、`ref` 指向被排除的表、bean / 全局字段全被排除。生成结束打印省略的条目与字段数。
  文档：`docs/CFGGEN_META.zh-CN.md`"导出分组"一节。
- **entity 生成器的两条前门规则钉住**（U-0091，C2）：`remote=managed` 但只嵌 `*entity.EntityBase` 时 `generate` 拒绝且不落 wire 文件；
  标记参数重复给出（`sync=true sync=false`）拒绝而非后者覆盖。`gen_promises_test.go` 两条；回退两处守卫各红。gap map 里其余五条
  是模板体内（生成到业务工程里）的守卫，由 roost-core `entity` 的 RemoteCommit 契约测试覆盖，不在本仓单测范围。
- **cfggen 元数据的字段级规则钉住**（U-0090，C2）：bean 重复字段 / 两字段映射同一 Go 字段（`item_id` 与 `itemID` 都是 `ItemID`）/
  bean 字段带 ref 或 index、ref 指向未声明的表、table / global 重名、无字段、表字段重复 / 同 Go 字段、`file` 逃出数据目录。
  `meta_promises_test.go` 一条，逐条按文案断言；回退九处守卫各红。
- **`roost add` 各 kind 的参数守卫钉住**（U-0089，C2）：非法名、service 重复、mod 指向未知 service、access 名非 player /
  多 service 未指定 / 已存在、transport 非 tcp / 归属 service 不符 / 已存在、saga 多 service 未指定 / 未知 service、protocol
  handler 与 group 非法、handler 缺 nest 特性；每次拒绝后 `roost.yaml` 字节不变。`add_promises_test.go` 两条；回退 14 处守卫 12 处红。
  `unknown kit mod` 与 `unsupported access layer` 两处是前门冗余：去掉后下游 `Manifest.Validate` / `resolveMods` 以同一文案拒绝并回滚清单。
- **nest 处理器的接收者与 target 声明规则钉住**（U-0088，C2）：值接收者（会在副本上调用）、`target` 与 `targets` 同时给出、
  target 名为空。`handler_promises_test.go` 一条；回退三处守卫各红。"非 error 返回值跟在 error 之后"那条经 Go 语法不可达
  （先被"error 必须是唯一末位返回值"拦下），记为冗余。
- **protocol 定义的取值与引用规则钉住**（U-0087，C2）。U-0032 钉的是结构规则；这次是枚举值超 int32 / 非整数字面量、字段缺 pb 号、
  oneof 名非标识符、消息的请求 / 响应结构体不存在、同一枚举在目录内两处声明。`definition_promises_test.go` 一条；回退六处守卫各红。

### Fixed

- **`category=` 指向业务包常量时生成物现在会导入那个包**(U-0166,C2;RR-20260910-05,T-60)。`collectEntityImports` 收集了 EntityKind、SyncTopic、packer、component、dao 的限定包,漏了 M-05 新增的 category:该包只在 category 表达式里出现时,生成物用了它却没导入,消费者报 `undefined: view`。M-05 的测试只用了 `entity.EntityCategoryPlayer` 这种 core 常量,而 core entity 包是无条件导入的,恰好绕过这个洞。`category_import_promises_test.go` 改为解析生成物、要求每一个 `pkg.Sel` 限定名都有对应 import,而不是只找某个字符串,这样将来往标记里再加字段也覆盖得住;修前红。修复记录见 roost-core `docs/bugfix/RR-20260910-05.md`。
- **聚合注册的固定 entity 导入不再与业务包别名撞名**(U-0167,C2;RR-20260910-06,T-61)。M-05 让模板无条件导入 core 的 entity 包(末尾要调 `ValidateEntityRegistry`),而别名分配器的保留集仍是 registry / sync / fmt / err:注册包路径以 `/entity` 结尾时被分到 `entity` 别名,生成物同时出现两个 entity,消费者报 `entity redeclared in this block` 与 `undefined: entity.RegisterEntity`。保留集抽成具名的 `aggregateReservedNames` 并加入 entity,注释写明它必须与模板的 import 块同步、以及 `err` 为什么在里面(那是错误检查绑定的局部变量,不是 import)。`entity_alias_promises_test.go` 修前红。修复记录见 roost-core `docs/bugfix/RR-20260910-06.md`。
- **多实体的包配合显式 `-output` 现在被拒绝而不是静默覆盖**(U-0162,C2;RR-20260909-06,T-56)。生成循环对每个实体都用同一个 `-output` 路径,后写的覆盖先写的连同伴随的守卫测试文件;工具 exit 0 并两次报告"生成同一路径",而消费者编译报 `undefined: RegisterEntity` —— 活下来的那个文件不是按名排序的第一个,包级 `RegisterEntity` 随被覆盖的文件一起消失。`-output` 是给 `go:generate` 单文件模式用的,和"一个包多个实体各占一个文件"在语义上冲突。现在在写任何文件之前拒绝,报错点出目录、实体个数与实体名并说明拿掉 `-output` 就能并排生成,失败的一次运行不留半成品;单实体配 `-output` 照旧工作。`output_multi_entity_promises_test.go` 修前红。修复记录见 roost-core `docs/bugfix/RR-20260909-06.md`。
- **`roost entity` 对同包多个实体不再生成重名的注册符号**（U-0160，C2；RR-20260909-04，T-55）。每个生成文件此前都声明包级 `registerEntityOnce` / `RegisterEntity`，两个实体同包时生成成功、消费者 `redeclared in this block`。现在每实体生成 `register<Name>EntityOnce` 与 `register<Name>Entity()`，带 `//roost:register phase=entity` 的包级 `RegisterEntity` 只进按名排序的第一个实体文件、逐个调用兄弟；registry 收集器与 `pkg.RegisterEntity()` 调用方式不变，已有消费者重新生成即可。`multi_entity_package_promises_test.go` 修前红。修复记录见 roost-core `docs/bugfix/RR-20260909-04.md`。
- **`roost project upgrade --consolidate` 对单行 import 的混合分流不再生成非法 Go**（U-0156，C2；RR-20260908-03，T-52）。旧业务文件只有一条不带括号的 `import "…/roost-kit/redis"` 又同时用到留在 kit 的 Mod 胶水与搬到 core 的符号时，新 ImportSpec 的文本此前被无条件塞在第一个 spec 之后，得到一条顶层裸露的 `coreredis "…"`，`format.Source` 报 `expected declaration`，该文件升级失败。
  现在先找到第一个 import 所在的声明：有括号块照旧插在块内；没有括号就在该声明之后另起一个 `import ( … )` 声明。`consolidate_single_import_promises_test.go` 修前红。修复记录见 roost-core `docs/bugfix/RR-20260908-03.md`。
- **发布清单对齐 core v1.13.0**（`security.RateLimiter` 改用 x/time/rate，新增依赖 `golang.org/x/time`；API 不变）。生成工程的 `versions.core`
  缺省仍取 latest，下限不变。
- **`scripts/gapmap.sh` 收尾不再 `git clean`**：采样后只还原被改动的**已跟踪**文件；未跟踪文件（比如正在写的测试）原样保留并提示。
  之前的版本把采样期间新建的一个测试文件删掉了。
- **发布清单对齐 core v1.12.1**（73 个提交的测试基线与 gap map 工具，无 API 变化）。生成工程的 `versions.core` 缺省仍取 latest，下限不变（v1.12.0）。
- **gap map 工具**（与 roost-core 同一份拷贝）：`scripts/gapmap/revertsample.py`、`scripts/gapmap.sh`、`nightly-gapmap` 工作流。
  五仓至此都有每日的承诺回退采样报告。
- **发布清单对齐 kit v1.12.6**（Redis 客户端尊重 ctx 截止期、`nats.ignore_discovered_servers`）。生成工程的 `versions.kit` 缺省仍取 latest。
- **发布清单对齐 kit v1.12.5**（v1.12.4 的 integration 构建红：测试辅助函数重名；另含 NATS 半开故障切片与
  `nats.ignore_discovered_servers`）。生成工程的 `versions.kit` 缺省仍取 latest，下限不变。
- **发布清单对齐 kit v1.12.4**（JetStream 结算失败 / outbox 认领失败可见；toxiproxy 故障矩阵）。生成工程的
  `versions.kit` 缺省仍取 latest，下限不变（v1.12.2）。
- **托管服务的文档与检查**（方向二 ③）。用到框架服务的工程多生成 `docs/SERVICES.zh-CN.md`：子命令、配置文件、必填项、
  协作者文件、业务侧类型化访问器、本地运行顺序。`roost project doctor` 新增 `collaborators:<服务>` 项：协作者文件
  里还含生成的 fail-closed stub（"is not configured"）时报失败并说明该服务会拒绝一切请求；`roost project next`
  在业务链完成后也提示实现它们。
- **game 模板第二切片：Player 与 World 实体**。`-template game` 现在给业务 Service 补 nest 运行时，生成
  Player、World 两个 Entity 及其 lifecycle，并生成 `game/lifecycle/world_singleton.go`（`WorldUniqueID = 1`、
  `EnsureWorld`）与一个在 `Init` 里确保 World 存在的 game Service：World 是**进程内单例**——每个 game
  进程自己的一份，首次启动创建、之后加载，没有 World 的 game 进程不启动。本地验证：对真实 Mongo 副本集 +
  NATS 集群 + Redis 连续两次启动 game 进程，`Init` 均通过、进程存活。

### Changed（测试质量）

- **dao 生成器层的四条拒绝补上测试**（U-0041，C2）：抽样回退发现 redis DAO 的"未实现的 mode"、"缺 key"、"缺 key 类型"
  与 Mongo DAO 的"未知 dbscope"四处守卫去掉后全绿（解析层的 tag 陷阱早有 `TestParseRejectsDaoTagTraps` 钉住）。
  现在四处各自按错误文本与 DAO 名断言，回退任一处对应用例变红。dao 包是 codegen 里测试密度最高的生成器包——
  这四处是解析器之后、模板之前的那一层，恰好落在两组测试之间。

### Fixed

- **webroute 对 `//roost:web` 的坏键与缺键指错方向**（U-0040，C2 / C5）。`methd=POST` 这样的拼错键被接受，随后报
  `unsupported method ""`；漏写 `method=` / `path=` / `body=` 也是同一条含混报错。现在按键报 `unknown marker option "methd"
  (known: method, path, body)` / `missing marker option "path"`。同时把解析器十二处拒绝逐条按错误文本钉住（原先只有
  "signature" 与 "duplicate" 两条测试）：裸词、空值、重复键、PUT、相对路径、xml、GET+json、raw 路由用了类型化请求、
  三种签名缺陷各自一条、同目录混包不落文件。回退四处守卫（两处新增、两处原有）对应用例各自变红。
- **entity 生成器对 `//roost:entity` 标记的三类错误不出声**（U-0039，C5）。① 标记后面两行内没有 `type X struct`（常见于
  标记与类型之间夹了多行文档注释、或标记放在 interface 上）——标记直接消失：不生成 wire 文件、不报错；② 参数名拼错
  （`remot=managed`）或裸词（`noPersist` 少了 `=true`）等同于没写，生成出来的是本地 / 持久实体；③ `remote=bogus`、
  `lifetime=forever`、`sync=ture` 各自回落到默认值。现在三类都在解析期按 `文件:行` 报错并列出合法键 / 合法值；`id=`
  （`roost add entity` 写入）在合法键内。六条测试：两条未挂接、两类坏键、四种坏值各自拒绝，六种文档过的写法全部放行；
  三处守卫各自回退对应测试变红。
- **eventgen 对两类它看得见的问题不出声**（U-0038，C5）。① 扫 `-game` 目录时解析不了的源文件被 `return nil, nil`
  跳过：该文件里的 `DealEventXxx` 从生成的分发里消失，事件永远不投递、没有任何报错；现在按文件报 `parse <file>: <pos>: …`。
  ② `DealEventGhost` 没有对应的 `EventGhost` 声明时照样生成 `case *event.EventGhost:`——编译错误落在用户没写过的生成
  文件里；现在第一阶段解析到的声明集传给第二阶段，扫描后、写文件前按"文件: (接收者).DealEventGhost 没有 EventGhost"
  逐条报出、不写任何分发文件。三条测试钉住两条拒绝与一条放行；回退任一守卫对应测试变红。
- **`deploy/dev/docker-compose.yaml` 初始化的 Mongo 副本集成员地址是 `mongo:27017`**，而生成的服务配置在宿主机上
  拨 `127.0.0.1:27017`：驱动发现成员地址后去解析 `mongo`，宿主机解析不了，任何带 dataengine 的进程在开发机上
  都停在 `ReplicaSetNoPrimary … lookup mongo: server misbehaving`。成员改为 `127.0.0.1:27017`（容器内同样是本机）。
  已有工程 `make sync` 后需要 `docker compose -f deploy/dev/docker-compose.yaml down -v` 重建卷，旧卷里的副本集配置
  仍指向 `mongo`。这是启动门禁（framework-compat full 场景真启动 mail / game）首跑抓到的。
- **`add lifecycle` 对第二个 Entity 生成的文件与第一个重复声明 `FromRegistry`**，同包编译不过（U-0026）。
  入口改为 `<Entity>FromRegistry`（`PlayerFromRegistry`、`WorldFromRegistry`）；已生成的工程文件是业务所有、
  不会被改写。`TestGameTemplateScaffoldsWorldAndPlayer` 用 go/parser 检查 lifecycle 包无重复顶层声明。
- **默认生成的工程一个都起不来，根因在 kit**（U-0025）：dataengine / saga / remoteentity 的 `DependsOn` 写了
  非 Mod 名（`health`、`nats.jetstream`），app 按 Mod 名解析依赖。已在 roost-kit 修复并加守卫测试；生成器目录
  无需改动（health 不是 Mod）。这是模板工程第一次真正启动才发现的第三个"装配级"缺陷。

- **托管 roost-service 服务与 `-template game`**（方向二第一切片）。roost.yaml 新增
  `services.<name>.framework`（account / mail / match / chat 之一：该进程就是这个服务的 Server 加 owner
  Mod，redis、nats 自动补齐）与 `services.<name>.uses`（业务 Service 要调用的托管服务：装配 ClientMod、
  补 nats、生成 `internal/service/<name>/framework_clients_gen.go` 类型化访问器）；`versions.service`
  与 `-roost-service-version` / `upgrade -service`（下限 v1.5.1；旧清单没有该字段时按 latest）；go.mod
  与 `frameworkdeps` 只在用到时才带 roost-service。owner Mod 需要的协作者（身份校验、玩家 id 分配、
  名字规则、投递、频道策略、系统鉴权……）生成到 `internal/service/<name>/collaborators.go`，只生成一次、
  默认全部拒绝（fail-closed），项目自己实现。`roost project new … -template game` 一次生成四个托管服务并把
  第一个业务 Service 接上；生成工程的启动命令因此多出 account / mail / match / chat 四个子命令，部署
  产物（compose / k8s / shell）随 services 自动覆盖它们。本地验证：模板工程 `go build` / `go vet` /
  `generate --check` 通过，五个子命令对着真实 Redis + NATS 起得来。World / Player 实体是下一切片。

### Fixed

- **nest 生成器的四条校验补上测试**（U-0035，C2）：remote tag 缺快照类型、未知 `k=v` 选项、重复快照类型、同一源文件混用
  handler 接收者——八条回退中这四条全绿（"重复 alias"由两处检查互为冗余，任去一处仍红）。`promises_test.go`。
- **attribute 生成器的九条校验此前没有任何测试**（U-0034，C2）：字段超过 `max`、重复字段、重复公式、公式返回值个数与类型、
  引用未知字段、入参类型不符、公式无输入、公式成环——逐条临时去掉校验，唯一的一条测试仍绿。`validation_test.go` 表驱动
  九条。cfggen 的"key 必填"与"key 字段未声明"两条校验互为掩护（去掉任一条，另一条仍让测试红），bean 名是 Go 关键字
  一条无覆盖；新增按错误文本断言的测试。
- **tablegen：`unique="true"`、`min=` 与主键唯一性此前从未被执行**（U-0033，C6）。这些约束从 tag 读出、印进 CSV 的规则行，
  然后什么也不检查：重复的 id 被写进 JSON，生成的 loader 静默保留最后一行。现在 CSV → JSON 时按表声明校验：key 列与
  `unique` 列不得重复、数值不得低于 `min`，错误指明文件、字段与行号；`ref=` 是跨表引用，仍留给 loader。
  另四条已有的行为（必填空格报错、单元格解析错误带行列、`-force` 才能覆盖、跳过标题/类型/规则行）此前也没有测试，
  一并补上（`csv_rules_test.go`）。
- **protocol 生成器的六条结构校验此前没有任何测试**（U-0032，C2）：重复 struct、未导出类型、重复字段号、req/resp id 相等、
  枚举首值为 0、枚举重复值名——逐条临时去掉校验，四条原有测试全绿。`validation_test.go` 用表驱动逐条破坏合法定义，
  五条现在红；"req id 必须等于 resp id"是死分支（解析时 `RespID` 直接取 id），记录为观察。
- **registry / errcode 生成器的三条承诺补上测试**（U-0030，C2）。对两个包的注释承诺临时回退：无法解析的源文件应报错而非静默跳过
  （跳过会让聚合少注册）、返回 error 的注册函数在生成的 `RegisterAll` 里必须检查并包装、重复错码必须报错——三处去掉后原有测试
  全绿；`registry/promises_test.go`、`errcode/promises_test.go` 钉住。未知 phase、方法上的标记两条已有测试红。
- **servicerpc 生成的 ClientMod 在真实进程里装配不起来**（U-0024，C4）。模板里 `ClientMod.DependsOn`
  返回 `mods.ModBus`——那是总线 **capability** 的名字，而 app 按 Mod **名字**解析依赖，没有任何 Mod
  叫 `bus`：把 `xxx.NewClientMod()` 与 kit 的 nats Mod 装进同一个进程，启动即
  `unknown mod dependency "bus"`。roost-service 的八个客户端全部如此，直到 `-template game` 生成的
  工程第一次真的启动 game 进程。现在依赖发布总线的 `mods.ModNats`；golden 随之更新，
  `TestTheGeneratedClientDependsOnTheModThatPublishesTheBus` 钉住。roost-service 已用修正后的生成器重生成。
- **生成的 `deploy/docker/docker-compose.prod.yaml` 在 `docker compose config` 下报
  `service "game" refers to undefined volume configs/service/config.game.yaml`**。配置文件挂载
  用的是短语法 `${ROOST_CONFIG_ROOT}/config.<svc>.yaml:/etc/roost/config.yaml:ro`；compose 把不以
  `/`、`./`、`../` 开头的 source 当作**命名卷**，于是 `ROOST_CONFIG_ROOT=configs/service`（生成的
  Makefile、生成的 CI、本仓三条工作流都这么传）渲染成对一个未定义卷的引用。每个生成工程的 CI
  "production compose renders" 步骤因此必红。现在两个挂载都用长语法并显式 `type: bind` /
  `type: volume`；同时生成的 Makefile 改传 `$(CURDIR)/configs/service`、生成的 CI 改传
  `${{ github.workspace }}/configs/service`——compose 对相对 bind source 是相对 compose **文件**
  所在目录（`deploy/docker/`）解析的，相对路径即使通过校验，`up` 时挂的也是错目录。
- **生成的 `.github/workflows/release.yml` 带着与本仓相同的两处 shell 债**（`! grep` 独立语句 SC2251、
  `sha256sum *.tar.gz` 裸通配 SC2035）。v1.13.1 的 consumer-acceptance 在 `git init` 之后 actionlint 第一次
  真正跑起来，立刻把它们报了出来——这一步在此之前从没检查过任何东西。模板已改；
  `TestGeneratedWorkflowsHaveNoBareNegationsOrGlobs` 扫所有渲染出的工作流。清单 codegen 版本随之 v1.13.2。
- **生成的 Dockerfile 用 `golang:1.25` 构建一个 `go 1.27.0` 的工程**，镜像构建在 `go mod download` 就停：
  `go.mod requires go >= 1.27.0 (running go 1.25.14; GOTOOLCHAIN=local)`。生成的 go.mod 写
  `go 1.25.0`，随后 `go get` 框架时被抬到 1.27.0，Dockerfile 的 `ARG GO_VERSION=1.25` 却没人跟。
  现在 go.mod 指令、Dockerfile 构建镜像、新手文档三处共用 `generatedGoVersion`（1.27），
  `TestGeneratedGoVersionMatchesTheGeneratorsOwn` 把它钉在本仓 go.mod 的 go 指令上。
- **生成的部署脚本过不了 shellcheck**（生成工程 CI 的 "deployment shell syntax" 步骤）：
  六个脚本的 `ROOT=$(CDPATH= cd -- …)` 报 SC1007，改为 `CDPATH=''`；`install.sh` / `rollback.sh`
  用 `case " game gate " in *" $SERVICE "*)` 在一个常量词上做 case 报 SC2194，改为在 `$SERVICE`
  上做 `case "$SERVICE" in game|gate)`——顺带消除了名字互为子串的服务被误放行的可能。有状态
  服务的 WAL 目录守卫在没有有状态服务时整块省略，而不是渲染成一个空的（非法的）case。
- **本仓四条工作流自 v1.12.1 起全红，五个互不相关的原因**（收敛单元 U-0015）：
  ① `release.yml` 三处 `! grep …` 独立语句不受 `set -e` 约束（SC2251）——所谓的发布卫生检查
  从来没有真正失败过；改为 `if grep …; then exit 1; fi`。② `sha256sum *.tar.gz` 裸通配（SC2035）。
  ③ `framework-compat` 的 `minimum` 依赖集钉在 core/kit v1.8.0，而生成器下限是 v1.10.0，
  `project new` 直接拒绝——两处字面量一个事实，新增测试把工作流里的最小集与 `minimumVersions`
  钉在一起。④ `upgrade-compat` 用 codegen v1.9.0 / v1.10.0 造历史工程，那两版写的是改名前的
  `cube-core` / `cube-kit` 模块路径，`go get` 永远解析不了；矩阵改为 v1.11.0 / v1.12.1（首个写
  roost-* 路径的版本起），并有测试守住下界。⑤ `framework-release` 的 consumer-acceptance 在生成
  工程目录跑 actionlint，那里没有 `.git`，actionlint 以 "no project was found" 退出 3；先 `git init`。
  以上五项加生成器两项共七条断言进 `internal/roost/deploy_hygiene_test.go`。

### Changed

- **版本下限抬到 core v1.12.0 / kit v1.12.2 / skill v1.10.3 / service v1.5.2**（codegen v1.7.0 不变）。kit 的下限是含 Mod 依赖名修正（U-0025）的第一个版本：启动门禁的 minimum 场景证明 kit v1.12.0 生成的工程起不来。service 的下限是含 ClientMod 依赖修正（U-0024）的第一个版本：钉 v1.5.1 的工程一用 `uses` 就起不来。四个框架
  模块是一起演进的：roost-service v1.5.1 要求 core / kit v1.12.0，钉在 v1.10.0 的工程一旦托管框架服务就解析不了
  （framework-compat 的 minimum × full 场景在 `-template game` 后立刻红）。下限从此是"能整体解析的最老组合"
  而非各模块自己的最老 tag。钉了旧版本的 roost.yaml 会在校验时得到明确的升级提示。
- **`ci/framework-release.yaml` 对齐到当前正式 tag**：kit v1.12.1（v1.12.0 的 tag CI 因既有问题红，
  修复后补打）、skill v1.10.3、service v1.5.1、codegen v1.13.1；core 仍 v1.12.0。此前清单指向的
  v1.12.0 / v1.10.1 是有效但非最新的 tag。收敛待办 B-13。
- **生成的 Kubernetes base 移到 `deploy/k8s/base/`**，overlay 改引用 `../../base`。kustomize
  v5.7+（kubectl 1.34+ 内置）拒绝 base 目录是 overlay 祖先的布局：
  `cycle detected: candidate root deploy/k8s contains visited root deploy/k8s/overlays/staging`
  ——旧布局（`deploy/k8s/kustomization.yaml` + `overlays/*/../..`）在新 kubectl 上一个对象都渲染
  不出来，生成工程 CI 的 "kubernetes manifests render" 步骤和 `deploy/k8s/deploy.sh` 一起失效。
  overlay 同时把已弃用的 `commonLabels` 换成 `labels`（`includeSelectors: true`，选择器语义不变）。
  **升级现有工程**：`make sync`（或 `roost project sync`）写入 `deploy/k8s/base/*`，并删除旧位置的
  六类受控清单（`kustomization` / `namespace` / `service-account` / `network-policy` /
  `<service>.yaml` / `<service>-pdb.yaml`）。旧清单没有生成头，sync 原本认不出它们是 codegen 的产物，
  第一次试跑 `removed=0`——现在按固定文件名加 `roost` 命名空间识别，并且 base/ 下的新清单都带上了
  `# Code generated` 头，下次再搬家就走通用规则。你自己手写的 `deploy/k8s/*.yaml` 不动；旧位置的
  `secret.<service>.example.yaml` 从来不受控，留在原地，自行删除；`secret.<service>.local.yaml`
  需要手动移到 `deploy/k8s/base/`（`.gitignore` 的忽略项已随之更新）。
  `TestKubernetesBaseIsNotAnAncestorOfItsOverlays` 在有 kubectl 的机器上真的跑 `kubectl kustomize`
  两个 overlay 并把弃用告警算作失败；`TestSyncRemovesTheLegacyKubernetesBase` 覆盖迁移。
- **`ci/framework-release.yaml` 加入 `framework.service`**，roost-service 成为发布链的一层：
  `framework verify` 同样下载它并拒绝 replace / 伪版本，GitHub output 多一个 `service`，
  缺少该字段的清单不再通过校验。清单里的版本同时从 core v1.9.1 / kit v1.9.2 / skill v1.9.1 /
  codegen v1.10.0 更新到当前正式 tag（core v1.11.3 / kit v1.11.3 / skill v1.10.0 /
  service v1.4.0 / codegen v1.12.1；此后逐版对齐：service v1.5.2 / codegen v1.13.3，kit v1.12.2 / codegen v1.13.4，
  codegen v1.13.5，kit v1.12.3 / service v1.5.3 / codegen v1.13.6，service v1.5.4 / codegen v1.13.7）——它落后了两个次版本，release 门禁一直在校验旧组合。
  `release.yml` 先把 `SERVICE` 传到 smoke 步骤；`project new` 的 `-roost-service-version`
  随 game 模板一起来。

### Added

- **`servicerpc` 生成器**（`cmd/servicerpc`）：从**手写的服务接口**生成跨进程传输层
  ——线上类型、handler 注册、打字的 `BusClient`、`Server`、`ClientMod`、capability
  包装。输入是打在接口上的 `//roost:rpc service_type=... capability=...`，不另立 def
  文件。产出 `<接口名小写>_rpc_gen.go`，`-check` 模式给 CI 用。

  从接口生成而不是从单独的 IDL 生成，是这个生成器唯一值得存在的理由：它省的打字量
  不多，但让**接口与传输层漂移在结构上不可能**，而不是可被检测到。

  拒绝规则和生成物一样重要（清单见 README）。每一条都对应一个真实踩过的坑，其中
  三条值得记：

  - **未导出字段**：任何 codec 都会静默丢掉它。`chat.ChannelRef` 的 key 字段是故意
    未导出的（ref 只能来自 `Resolve`），所以它根本过不了总线——对面拿到的 ref 指向
    空，而不是报错。生成器点名说的是 `ref.key`，不是 `ref`。
  - **首参必须是 `context.Context`**：这条看着像形式要求，实际是个信号。
    `platform.ValidateSession(playerID, token)` 没有 ctx，因为它不做任何 I/O，只用本
    进程已有的密钥重算一个 MAC；把它做成一次往返意味着全集群的会话校验都排在一个
    进程后面。一个什么都不碰的方法没有理由上总线。
  - **包里必须有 `ErrRequestInvalid`**：生成的 handler 需要一个带码的错误来回答"这
    一帧我读不懂"。这条规则第一版是**空转的**——`hasRequestInvalid` 在 `parseFile`
    返回之后才赋值，而检查跑在 `buildService` 里面。golden 测试抓到了它，之后所有
    包级事实都收进一个 `pkgFacts` 结构体，规则读不到零值。

- **`servicerpc`：两条包级拒绝规则**，都由真实事故推出来：

  - **生成名与包内已有声明撞名**。`roost-service` 的 `account` 包有
    `type Server struct`（服务器列表里的一行），而生成的进程壳也叫 `Server`：两个
    文件放一起编不过，而编译器给的是 "Server redeclared in this block" 并指向生成
    的文件——它告诉你重复在哪，不告诉你为什么在那儿、以及哪一边能动。生成的那边
    不能动（`pkg.Server`/`pkg.CapabilityName` 要在每个服务里读法一致），所以现在
    点名拒绝并说清该改哪个。检查覆盖类型、函数、常量、变量，导出与未导出都算。

    配套的 `emittedNames` 是一张手写清单，因此有一条测试**解析生成的 golden 文件**
    并要求清单覆盖它实际声明的每个包级名字：漏列一个名字的后果很安静——规则继续
    接受那个包，失败以"redeclared"的形式出现在生成代码里，正是规则要防的那件事。

  - **一个包里只能有一个被标记的接口**。这条不是绕不过去的限制，而是把一件早就成立
    的事说出来：`app.Service` 每进程一个，所以两个被标记的接口就是两个独立部署的
    东西，而那应该是两个包。备选方案（按接口给生成名字加前缀）会让 `pkg.Server` 在
    有些包里叫 `pkg.FooServer`，把成本转给每个服务的每个读者。`roost-service` 的
    `global` 包正是被这条顶出来拆成了 `global/` + `global/activity/`。

- `scripts/pretag.sh`：打 tag 之前的发布预检（tag major 与 module 路径后缀一致、
  tag 未存在、无 replace、工作区干净、`GOWORK=off` 下 build/vet/test 通过）。
  由 tag push 触发的 CI 运行在 tag 已存在之后，能报告但阻止不了。

### Fixed

- **`servicerpc -check` 此前根本不检查生成物。** 它在拒绝规则跑完之后就 `return nil`，
  于是对一个**过时的、被手改过的、或由另一个版本的生成器产出的**生成文件一律退出 0
  —— 而它在两份 README 里被写成"CI 的漂移门禁"。

  一个不可能失败的门禁比没有门禁更坏：它报告"提交的传输层与接口一致"，而没有任何人
  看过。发现方式是故意往一个已提交的生成文件尾部追加一行注释，然后看 `-check` 退出 0。

  现在 `-check` 会生成并逐字节比对，任何差异非零退出，并且**区分"缺失"与"不一致"**
  ——前者是没人跑过生成器，后者是文件被改过或由别的版本产出，两者的修法不同。
  拒绝规则仍然先跑：一个过不了总线的类型是设计问题，作者要的是那个答案而不是一份 diff。
  三条变异（回到旧行为 / check 模式也写文件 / 不区分缺失与不一致）全部验证变红。

- **`OwnerCapabilities` 在 owner-only 名下注册的是包装器，导致五个服务的进程起不来。**

  拥有者 Mod 注册两个 capability：公开名给消费方（放**包装器**，这样消费方绑不到实现
  类型），owner-only 名给 `Server` 判断"本进程是不是拥有者"。此前两个名字下放的是**同一个
  包装器**。

  后果是 `Server.Service()` 交给手写 `run` 钩子的是包装器，而钩子**必须**断言具体类型
  ——它要调的正是那些**刻意不上总线**的拥有者专属方法（扫过期、重试发货、裁剪保留）。
  于是 `match`/`platform`/`chat`/`global/activity` 从 `run` 返回错误，`Serve` 失败、
  进程起不来；`session` 那条是**写在 ticker 里的裸断言**，会在 30 秒后 panic，而且只在
  真的配了 owner 的部署里 panic —— 这比立刻失败更坏。

  没有任何测试抓到，因为**没有任何测试调用过 `Serve`**。原有的
  `TestOnlyTheMailServerRegistersHandlers` 只覆盖一个包的 `Init`，而 `Init` 恰好是好的那半。

  改法：owner-only 名下放**未包装的实现本身**。这不削弱消费方的保证 —— owner-only 名
  只存在于拥有者进程，消费方查它在拆分部署下**什么都查不到**，在 lookup 处大声失败，
  而不是在类型断言处安静失败。

### Changed

- **`go.mod` 的 go 指令 1.26.5 → 1.27.0**，四仓（core / kit / codegen / service）与
  `go.work` 统一到同一条线上。取 1.27.0 而不是当前最新的 1.27.1：一个补丁级的 go 指令
  什么都买不到，还会让停在 1.27.0 的工具链去下载一个新工具链。

  这条不是整理格式，它有一个**外溢后果**必须写下来。消费方仓库把本仓当**工具依赖**
  接进来时（`go get -tool .../cmd/servicerpc`，让 `GOWORK=off` 下也能 `go generate`），
  `go mod tidy` 会把消费方自己的 go 指令顶到本仓的高度，而且**手工按回去不管用**——
  下一次 `go mod tidy` 又顶回来。`roost-service` 就是这么从 1.25.0 变成 1.26.5 的。
  所以一个生成器的 go 指令是**每个消费它的仓库都要满足的下限**，不是本仓的私事。

- **`ci/framework-release.yaml` 的 `consumer_go` 从 `[1.25.x, 1.26.x]` 改为 `[1.27.x]`**，
  `framework-compat.yml` 的矩阵与 `go work edit -go=` 同步。

  这是上一条的直接代价，值得单独列出来而不是藏在"顺带"里：**1.25.x / 1.26.x 两条
  consumer lane 没了**。core 和 kit 的 go 指令是 1.27.0，那两个工具链构建不了它们——
  留着那两行不是"还在测老版本"，是让 release 矩阵红着。compat workflow 里那句
  "The framework runtime modules support the lower consumer lane" 的注释也随之作废，
  已改写成为什么不能再往下 pin。

### Changed（破坏性：源码标记 //cube: → //roost:，模块路径与版本下限）

- 生成器读取的源码标记从 `//cube:<kind>` 改为 `//roost:<kind>`（entity、dao、redisdao、
  component、nest、protocol、msg、object、table、attribute、web、reverse_proto、register），
  生成的 .proto 溯源注释 `cube:source=go_def` → `roost:source=go_def`。**旧拼写本版仍被接受**：
  全部解析器经新的 `internal/marker` 包匹配两种前缀，`roost generate` 对仍在用 `//cube:` 的文件
  打印一次弃用告警并列出路径；下一个大版本移除。`roost doctor` 同样接受两种拼写。
- 生成代码的 import 路径与 catalog 改为 `roost-core`/`roost-kit`；`roost.yaml` 的 `versions.core`/
  `versions.kit` 最低版本提升到 v1.10.0（新模块路径下的首个版本）。
- protocol 生成的默认 proto 包名 `cube.protocol` → `roost.protocol`（仅在未显式指定时生效）。
- `roost add skill` 生成的技能定义使用 schema `roost.skill/v2`（随 roost-skill 改名）。
- 生成的 Redis DAO 默认 key 前缀 `cube:redisdao` → `roost:redisdao`（仅在 `//roost:redisdao` 未写 `prefix=` 时
  生效）。**不做兼容读取**：依赖默认前缀的已有项目升级后读不到旧 key，请在标记上显式写
  `prefix=cube:redisdao` 沿用，或迁移数据。

### Changed（生成项目结构）

- **`game/bootstrap/` 并入 `internal/registry/`。** nest 聚合器改生成到
  `internal/registry/nest_gen.go`（package registry），`nestOnce` 占位文件不再生成——
  `RegisterAll()` 已统一持有 `sync.Once`。聚合器对同包注册函数不加限定符调用（包不能导入自己）。
  迁移守卫新增对旧 `game/bootstrap/nest.go`、`register.go` 的识别。一个项目自此只有一处
  "启动时把东西接起来"的地方。
- 生成的文档新增目录约定四条：`game/` 玩法与实体；`internal/` 框架接线（bootstrap、registry、
  access、service）；每类定义在自己的顶层 `<kind>/def`；`configs/` 配置。
- **刻意不改的**：webroute 仍在 `service/web`。移到 `internal/access/web` 更一致，但 webroute
  生成器按目录扫描 `//roost:web`，改目录会让已有项目的路由**静默**不再被扫描。

### Added
- **静态注册统一为一份生成的清单。** 新增 `//roost:register` 标记与
  `internal/registry/generated.go` 聚合器（由 `roost generate` 生成，无条件运行且排在
  最后——它要收集前面生成器刚写出的标记）。`bootstrap.New()` 在 `app.New()` **之前**
  调用 `registry.RegisterAll()`。
  - **为什么不是一个新 mod**：静态注册必须在任何 mod 的 `Init` 之前完成。entity
    builder 若在某个 mod 的 `Start` 里注册，另一个在 `Provide` 里解析实体的 mod 就已经
    晚了。放在 `app.New()` 之前，这个排序问题用构造消除，不需要再引一个 mod 去管。
  - **为什么用标记而不按类型推导**：component 的叶子函数是业务手写的，命名不统一
    （`RegisterComponent`、`RegisterScheduler`、`RegisterBasicMissions`…），无法按约定
    推导。让注册函数自己声明阶段，一个机制覆盖全部类别，新增一类不必改 codegen。
  - 阶段固定序：`pre` → `kind` → `config` → `component` → `entity` → `protocol` →
    `nest` → `route` → `post`。顺序是语义的：`kind` 必须早于 `entity`（注册 builder 时
    要解析 kind→category），`config` 必须早于任何读配置表的注册。`pre`/`post` 让业务的
    "必须最先/最后"有位置，因此不需要另外一个 custom 钩子文件。
  - 输出**确定**：阶段 → `order` → import 路径 → 函数名，不受文件遍历或 map 顺序影响。
  - 标记无法执行时**直接报错**而非跳过（缺 `phase`、未知 `phase`、未知选项、
    非整数 `order`、带参数、未导出、返回值不是空或单个 `error`、打在方法上、同名重复标记）——
    静默丢掉一个注册，症状是很远处的一个 nil。
  - 生成的 entity wire 代码自带 `//roost:register phase=entity`，entity 因此走同一条路径。
  - **迁移守卫**：检测到手写聚合器仍在（`game/bootstrap/register.go` 的 `RegisterAll`、
    `game/entities/register/`、`game/components/register/`）时拒绝生成并给出迁移步骤。
    两个聚合器并存不会重复注册（每个叶子自带 `sync.Once`），但下次加实体的人不知道
    该改哪个。守卫按**内容**判断，不只看路径存在。
  - 12 条测试，含"生成物必须能解析且 import 了它调用的一切"这条——它抓的正是模板
    产出 `fmt.Errorf` 却没 import `fmt` 这一类。5 条变异验证。
- `generator` 新增 `Always` 字段：无条件运行，且不受 `--changed` 的前缀过滤影响。
- 四个阶段已接入：`entity`（生成的 wire 代码自带标记）、`component`（业务手写函数打标记）、
  `config`（`tablegen` 与 `cfggen` 各产出一个带标记的无参包装 `RegisterConfigData()`，
  内部用默认 registry；带参的 `RegisterGeneratedConfigData` 保持导出供测试与自建 registry 使用）、
  `nest`（生成的 `RegisterNestHandlers()` 打标记）。生成的 `bootstrap.New()` 相应删掉了
  对 nest 与 config 的显式调用——两份清单并存正是聚合器要取消的东西。
- **protocol 与 webroute 不并入**，因为签名说明它们不属于层 A：
  `RegisterPlayerProtocols(*player_agent.ProtocolRegistry, *app.Registry) error` 要 app registry，
  `RegisterRoutes(webroute.Registerer, *Service) error` 要活的 Service 实例。两者都不能在
  `app.New()` 之前运行，而它们今天已经在正确的位置（前者走一个 `app.IManager` 与生成的
  access mod，后者绑在 web service 上）。

### Added
- `manager` mod 进入 catalog 与**新项目默认 mod 集**。它是 per Service 的（内存单例按 Service 类型选择启动），因此：
  - 构造器按 Service 渲染成 `kitmanager.NewManagerMod(service<X>.Managers()...)`；
  - 生成 `internal/service/<name>/managers.go` 作为**业务可编辑、仅缺失时创建**的钩子（与 `service.go` 同规则，沿用 `nest` 的 `RegisterNestHandlers` 先例）——启动哪些 manager 是业务决策，mod 只拥有生命周期；
  - `shared_mods` 里声明 `manager` 会被 `Validate` 拒绝并提示应放到 `services.<name>.mods`：进程级的 manager mod 会把某个 Service 的单例在所有 Service 里都启起来。

### Changed
- 跟随 core/kit 的命名整理：`roost.yaml` 的 mod 键 `sync` 改为 `room`，feature 键
  `replication-quic/kcp/udp` 改为 `nettransport-quic/kcp/udp`。**旧键仍然接受**——
  `Manifest.Validate` 会把它们归一化成规范名（`Validate` 是所有构造路径的共同入口，
  因此手写 YAML、`project new` 的默认清单和 `upgrade` 的合并结果都覆盖到），
  下一次 `make sync` 写回规范拼写。未知的 mod/feature 仍然报错：归一化只映射已知的
  旧拼写，不会让校验变宽松。
- 生成代码的 import 路径随 kit 更新：`cube-kit/remote_entity` → `cube-kit/remoteentity`。


### Added — Data Engine
- DAO 生成器现在产出事务内 `PersistChange`、Put/Patch/Delete mutation、字段级 BSON patch、schema migration 与 tracker version 接受逻辑；普通 Entity wire 不再生成 Snapshot/RemoveSnapshot 写路径。
- Remote Entity wire 从当前 Nest transaction 领取变更，在 lease/fence 下冻结 commit，并只在权威远端确认后推进 DAO version；不再读取或回滚持久化 dirty。
- Project catalog 只保留 `dataengine` 持久化 Mod，自动接线 EntityAccess/Remote projection、Mongo/NATS 与独占 WAL 配置；Nest/Saga 和默认工程都会自动选择 Data Engine。

### Changed — Data Engine
- `DirtyTracker()` 的生成契约改为 `*dataengine.Tracker`。patch-only 生成代码要求 WAL writer v2；旧 Checkpoint/standalone NestWAL catalog、renderer 与帮助入口已经删除。

- Fixed `project doctor --strict` rejecting the codegen-owned `configs/examples/access.player.tcp.yaml` produced by the official player TCP workflow; the reference template now carries an explicit ownership marker and has a workflow regression test.

### Fixed
- Remote Entity 生成代码从 transaction outcome 的显式 delete intent 生成删除 commit，不再用内存 `IsRemoved()` 反推；生成的 DAO 并发容器 import 从 `cube-core/map` 迁移到语义明确的 `cube-core/safemap`。
- release 与 framework-compat 的 full consumer 场景不再请求已经删除的 `checkpoint`/standalone `nestwal` Mod，统一生成 `dataengine,nest,saga` 单持久化路径。
- framework-compat 的 source-head 矩阵支持显式指定 core/kit/skill 候选 ref，并逐仓库 checkout 到该 ref；框架跨仓库改动可在合并前验证同一候选集合，不再只能测试三个远端默认分支的偶然组合。
- `project doctor` 不再只检查文件结构：默认执行 `GOWORK=off go mod verify` 和只读 `go list ./...`，strict 进一步编译全部包和测试；命令带超时、有限错误输出和可执行修复提示，缺 go.sum 或不可编译工程不再显示全绿。相关正向 workflow 使用事务化生成先提交依赖元数据。
- 生成工程 Makefile/CI、framework-compat 与 upgrade-compat 流水线统一执行 core `glsvet`，Handler 裸 goroutine、隐式异步 Context 捕获和被忽略的 admission error 不再只依赖人工评审。
- Windows 创建工程的最终目录 rename 对杀毒/索引器短暂占用增加有界重试；只重试 access-denied 且目标仍不存在的瞬态条件，真实目标冲突和其他文件系统错误继续立即失败。

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
