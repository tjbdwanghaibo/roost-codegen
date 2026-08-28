package remote

import (
	"github.com/tjbdwanghaibo/cube-core/checkpoint"
	"github.com/tjbdwanghaibo/cube-core/entity"
)

const (
	EntityKindGuild    entity.EntityKind = 201
	GuildDAOCollection                   = "guilds"
)

func init() {
	entity.MustRegisterEntityKindDefs(entity.EntityKindDef{Kind: EntityKindGuild, Category: 6, RemotePolicy: entity.RemotePolicyManaged})
}

type GuildDAO struct {
	id      int64
	tracker checkpoint.DirtyTracker
	Name    string
}

func NewGuildDAO() *GuildDAO                               { return &GuildDAO{} }
func (d *GuildDAO) Id() int64                              { return d.id }
func (d *GuildDAO) SetId(id int64)                         { d.id = id }
func (d *GuildDAO) DbName() string                         { return "game" }
func (d *GuildDAO) CollName() string                       { return GuildDAOCollection }
func (d *GuildDAO) Dirty() entity.IDirty                   { return &d.tracker }
func (d *GuildDAO) CleanDirty()                            { d.tracker.SelfClean() }
func (d *GuildDAO) DirtyTracker() *checkpoint.DirtyTracker { return &d.tracker }
func (d *GuildDAO) MarshalPersist(uint64) []byte           { return []byte(d.Name) }
func (d *GuildDAO) ApplySync(data []byte) error            { d.Name = string(data); return nil }

//cube:entity entityKind=EntityKindGuild remote=managed
type Guild struct {
	*entity.RemoteEntityBase
	entity.DaoManager

	dao *GuildDAO `dao:"guilds"`
}
