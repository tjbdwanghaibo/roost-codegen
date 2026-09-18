package def

//roost:dao coll=heroes db=game dbscope=sid
type HeroDao struct {
	Name    string
	Level   int32
	Exp     int64
	LoginAt int64 `dao:"persist"`
	Tmp     int   `dao:"-"`
	Items   map[int64]int32
	Friends []int64
	Pos     Position
	Equips  map[int64]*EquipInfo
}

type Position struct {
	X int32
	Y int32
}

type EquipInfo struct {
	Level int32
	Star  int32
	Gems  map[int32]*GemInfo
	// Runes and Core lock the other two shapes a nested child can take
	// (slice element, single pointer): notification ownership has to be
	// established and released the same way in all three (RR-20260918-03).
	Runes []*GemInfo
	Core  *GemInfo
	// Shape is a nested value: it is overwritten in place, so it is bound
	// but never unbound.
	Shape Position
}

type GemInfo struct {
	ID    int32
	Level int32
}
