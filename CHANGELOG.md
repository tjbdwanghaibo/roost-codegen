# Changelog

本文件从 v1.5.0 起维护；更早版本见 git 历史。

## [Unreleased]

### Added

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

- **版本下限抬到 core v1.12.0 / kit v1.12.0 / skill v1.10.3 / service v1.5.2**（codegen v1.7.0 不变）。service 的下限是含 ClientMod 依赖修正（U-0024）的第一个版本：钉 v1.5.1 的工程一用 `uses` 就起不来。四个框架
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
  service v1.4.0 / codegen v1.12.1）——它落后了两个次版本，release 门禁一直在校验旧组合。
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
