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
}

type GemInfo struct {
	ID    int32
	Level int32
}
