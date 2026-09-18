//go:build daoruntime

// U-0236 · C4 · RR-20260918-03：嵌套 child 的"谁通知谁"在替换与撤销之后必须与
// "当前可达的 child 集合"一致。
//
// 承诺：一次替换之后，只有留在字段里的 child 会把变更传播到这个父对象；一次
// 回滚之后，恢复出来的那个 child 重新通知，被丢弃的那个不再通知。旧行为：
// map / slice 的 setter 正常替换时确实解绑旧的、绑定新的，但 undo 闭包只把
// 字段值设回去，不碰 callback——于是回滚之后字段身份恢复了、运行时关系没恢复，
// 恢复出来的 child 后续的真实修改静默漏出持久化链；单指针分支更进一步，连正常
// 替换都不解绑旧值。
//
// 和 roundtrip_test.go 一样，这个文件不由 `go test ./...` 编译：它需要真的
// roost-core。scripts/dao-golden-runtime.sh 把它和 golden 放进一次性模块里跑。
package testdata

import (
	"context"
	"errors"
	"testing"

	"github.com/tjbdwanghaibo/roost-core/nest"
)

type ownershipCommitter struct{ records []nest.CommitRecord }

func (c *ownershipCommitter) Commit(_ context.Context, rec nest.CommitRecord) error {
	c.records = append(c.records, rec)
	return nil
}

// 三种 child 形状（map 值、slice 元素、单指针）各跑四种模式：正常替换后旧 /
// 新 child 改一次，回滚后旧 / 新 child 改一次。断言的是"通知有没有到父对象"，
// 不是"字段值对不对"——值先单独断言一次，免得把两种失败混在一起。
func TestNestedChildNotificationOwnership(t *testing.T) {
	for _, kind := range []string{"map", "slice", "pointer"} {
		for _, mode := range []string{"normal_old", "normal_new", "rollback_old", "rollback_new"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				parent := &EquipInfo{}
				oldChild := &GemInfo{}
				newChild := &GemInfo{}
				set := func(c *GemInfo) {
					switch kind {
					case "map":
						parent.SetGems(map[int32]*GemInfo{1: c})
					case "slice":
						parent.SetRunes([]*GemInfo{c})
					default:
						parent.SetCore(c)
					}
				}
				reachable := func() *GemInfo {
					switch kind {
					case "map":
						v, _ := parent.GetGems(1)
						return v
					case "slice":
						v, _ := parent.GetRunes(0)
						return v
					default:
						return parent.GetCore()
					}
				}

				set(oldChild)
				marks := 0
				parent.SetNotify(func() { marks++ })

				rollback := mode == "rollback_old" || mode == "rollback_new"
				if rollback {
					sentinel := errors.New("abort")
					_, err := nest.RunIsolatedTransaction(context.Background(), &ownershipCommitter{}, "replace",
						func() (any, error) { set(newChild); return nil, sentinel })
					if !errors.Is(err, sentinel) {
						t.Fatalf("transaction error = %v, want the sentinel", err)
					}
					if got := reachable(); got != oldChild {
						t.Fatalf("after rollback the field holds %p, want the original child %p", got, oldChild)
					}
				} else {
					set(newChild)
				}

				marks = 0
				want := 0
				if mode == "normal_old" || mode == "rollback_old" {
					oldChild.SetLevel(9)
				} else {
					newChild.SetLevel(9)
				}
				// 现在字段里躺着的那个 child 才应该通知：正常替换后是 new，
				// 回滚之后是 old。
				if mode == "normal_new" || mode == "rollback_old" {
					want = 1
				}
				if marks != want {
					t.Fatalf("parent notifications = %d, want %d", marks, want)
				}
			})
		}
	}
}

// 通知归属错了不只是多打 / 少打一次 dirty：回滚之后对恢复出来的 child 做的
// 真实修改会整条漏出持久化链——下一次事务提交时父 DAO 认为无事发生，
// committer 一条记录都收不到。
func TestRestoredChildStillReachesTheCommitRecord(t *testing.T) {
	for _, abort := range []bool{false, true} {
		name := "control"
		if abort {
			name = "after_rollback"
		}
		t.Run(name, func(t *testing.T) {
			hero := NewHeroDao()
			hero.SetId(42)
			equip := &EquipInfo{}
			child := &GemInfo{}
			equip.setGemsRawMap(map[int32]*GemInfo{1: child})
			hero.setEquipsRawMap(map[int64]*EquipInfo{1: equip})
			hero.Init()

			committer := &ownershipCommitter{}
			if abort {
				sentinel := errors.New("abort")
				_, err := nest.RunIsolatedTransaction(context.Background(), committer, "replace",
					func() (any, error) { equip.SetGems(map[int32]*GemInfo{1: {}}); return nil, sentinel })
				if !errors.Is(err, sentinel) {
					t.Fatalf("transaction error = %v, want the sentinel", err)
				}
			}
			if _, err := nest.RunIsolatedTransaction(context.Background(), committer, "update_restored_child",
				func() (any, error) { child.SetLevel(9); return nil, nil }); err != nil {
				t.Fatal(err)
			}
			if len(committer.records) != 1 {
				t.Fatalf("durable commit records = %d, want 1 (child level = %d)", len(committer.records), child.GetLevel())
			}
		})
	}
}

// 值类型的嵌套字段没有"旧对象"可解绑，但同样要在替换之后重新建立归属：赋值
// 是一次结构体拷贝，连 hook 一起覆盖掉，此前 Set<Field> 完全不绑，于是替换过
// 的子结构后续的修改再也到不了父对象。
func TestReplacedNestedValueStillNotifies(t *testing.T) {
	parent := &EquipInfo{}
	marks := 0
	parent.SetNotify(func() { marks++ })
	parent.SetShape(Position{})
	marks = 0
	parent.GetShape().SetX(3)
	if marks != 1 {
		t.Fatalf("parent notifications = %d, want 1", marks)
	}
}
