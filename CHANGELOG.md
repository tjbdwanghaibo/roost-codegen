# Changelog

本文件从 v1.5.0 起维护；更早版本见 git 历史。

## [Unreleased]

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
