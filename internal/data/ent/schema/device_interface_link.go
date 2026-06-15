package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/tx7do/go-crud/entgo/mixin"
)

// DeviceInterfaceLink is a discovered Layer-2 neighbor link from a local device
// interface to a remote port (e.g. a server NIC seen in a switch's bridge FDB).
//
// An interface can have MANY links: an LACP bond across an MLAG switch pair is
// learned on BOTH switches, so the same server MAC connects to a port on each.
// The flat remote_* columns on DeviceInterface only hold the single "best" link
// (kept for backward compatibility and hypervisor/manual links); the full set
// lives here.
type DeviceInterfaceLink struct {
	ent.Schema
}

func (DeviceInterfaceLink) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "ipam_device_interface_links"},
		entsql.WithComments(true),
	}
}

func (DeviceInterfaceLink) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			NotEmpty().
			Unique().
			Comment("Unique identifier"),

		field.String("interface_id").
			NotEmpty().
			Comment("Owning local interface ID"),

		field.String("remote_device_id").
			Optional().
			Comment("Connected neighbor device ID (e.g. the switch this port plugs into)"),

		field.String("remote_interface_id").
			Optional().
			Comment("Connected neighbor interface ID (e.g. the switch port)"),

		field.String("remote_port_name").
			Optional().
			Comment("Connected neighbor port name (denormalized for display)"),

		field.String("link_source").
			Optional().
			Comment("How the link was discovered: snmp_fdb, lldp, manual"),

		field.Int32("link_vlan").
			Optional().
			Nillable().
			Comment("VLAN the neighbor MAC was learned on (Q-BRIDGE FDB)"),

		field.Time("link_last_seen").
			Optional().
			Nillable().
			Comment("When the neighbor link was last observed"),
	}
}

func (DeviceInterfaceLink) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("interface", DeviceInterface.Type).
			Ref("links").
			Field("interface_id").
			Unique().
			Required(),
	}
}

func (DeviceInterfaceLink) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixin.Time{},
	}
}

func (DeviceInterfaceLink) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("interface_id"),
		// One link row per (interface, remote switch, discovery source).
		index.Fields("interface_id", "remote_device_id", "link_source").Unique(),
		index.Fields("link_last_seen"),
	}
}
