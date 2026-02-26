package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/tx7do/go-crud/entgo/mixin"
)

// DevicePackage represents an installed package on a device
type DevicePackage struct {
	ent.Schema
}

func (DevicePackage) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "ipam_device_packages"},
		entsql.WithComments(true),
	}
}

func (DevicePackage) Fields() []ent.Field {
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
			MaxLen(512).
			Comment("Package name"),

		field.String("current_version").
			Optional().
			MaxLen(255).
			Comment("Currently installed version"),

		field.String("available_version").
			Optional().
			MaxLen(255).
			Comment("Available version for update"),

		field.Bool("needs_update").
			Default(false).
			Comment("Whether the package has an update available"),

		field.Bool("is_security_update").
			Default(false).
			Comment("Whether the available update is a security update"),

		field.String("package_manager").
			Optional().
			MaxLen(32).
			Comment("Package manager: apt, dnf, apk, pacman"),

		field.String("description").
			Optional().
			Comment("Package description"),
	}
}

func (DevicePackage) Edges() []ent.Edge {
	return nil
}

func (DevicePackage) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixin.Time{},
		mixin.TenantID[uint32]{},
	}
}

func (DevicePackage) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "device_id", "name").Unique(),
		index.Fields("device_id"),
		index.Fields("needs_update"),
		index.Fields("is_security_update"),
	}
}
