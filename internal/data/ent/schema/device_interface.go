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

// DeviceInterface represents a network interface on a device
type DeviceInterface struct {
	ent.Schema
}

func (DeviceInterface) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "ipam_device_interfaces"},
		entsql.WithComments(true),
	}
}

func (DeviceInterface) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			NotEmpty().
			Unique().
			Comment("Unique identifier"),

		field.String("device_id").
			NotEmpty().
			Comment("Device ID"),

		field.String("name").
			NotEmpty().
			MaxLen(255).
			Comment("Interface name (e.g., eth0, ens192)"),

		field.String("mac_address").
			Optional().
			Comment("MAC address"),

		field.String("interface_type").
			Optional().
			Comment("Interface type (e.g., ethernet, wifi, virtual)"),

		field.Bool("enabled").
			Default(true).
			Comment("Is interface enabled"),

		field.Int32("speed_mbps").
			Optional().
			Nillable().
			Comment("Speed in Mbps"),

		field.String("description").
			Optional().
			Comment("Description"),

		field.Int32("if_index").
			Optional().
			Nillable().
			Comment("SNMP ifIndex — identifies a switch port / interface within its device"),

		// Layer-2 neighbor link (e.g. server NIC -> switch port), discovered via
		// the bridge forwarding database (FDB) or LLDP during a network scan.
		field.String("remote_device_id").
			Optional().
			Comment("Connected neighbor device ID (e.g. the switch this server port plugs into)"),

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

func (DeviceInterface) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("device", Device.Type).
			Ref("interfaces").
			Field("device_id").
			Unique().
			Required(),
		// Discovered Layer-2 links to remote ports. An interface can connect to
		// multiple switches (LACP bond across an MLAG pair). Cascade-delete the
		// links when the interface is removed.
		edge.To("links", DeviceInterfaceLink.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (DeviceInterface) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixin.Time{},
	}
}

func (DeviceInterface) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("device_id", "name").Unique(),
		index.Fields("device_id"),
		index.Fields("mac_address"),
		index.Fields("device_id", "if_index"),
		index.Fields("remote_device_id"),
	}
}
