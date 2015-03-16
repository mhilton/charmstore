// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package storetesting

import (
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v5-unstable"
	"gopkg.in/mgo.v2"

	"gopkg.in/juju/charmstore.v4/internal/mongodoc"
)

// EntityBuilder provides a convenient way to describe a mongodoc.Entity
// for tests that is correctly formed and contain the desired
// information.
type EntityBuilder struct {
	entity *mongodoc.Entity
}

// NewEntity creates a new EntityBuilder for the provided URL.
func NewEntity(url string) EntityBuilder {
	URL := charm.MustParseReference(url)
	return EntityBuilder{
		entity: &mongodoc.Entity{
			URL:                 URL,
			Name:                URL.Name,
			Series:              URL.Series,
			Revision:            URL.Revision,
			User:                URL.User,
			BaseURL:             baseURL(URL),
			PromulgatedRevision: -1,
		},
	}
}

// PromulgatedURL sets the PromulgatedURL and PromulgatedRevision of the
// entity being built.
func (b EntityBuilder) PromulgatedURL(url string) EntityBuilder {
	b.entity.PromulgatedURL = charm.MustParseReference(url)
	b.entity.PromulgatedRevision = b.entity.PromulgatedURL.Revision
	return b
}

// Build creates a mongodoc.Entity from the EntityBuilder.
func (b EntityBuilder) Build() *mongodoc.Entity {
	return b.entity
}

// AssertEntity checks that db contains an entity that matches expect.
func AssertEntity(c *gc.C, db *mgo.Collection, expect *mongodoc.Entity) {
	var entity mongodoc.Entity
	err := db.FindId(expect.URL).One(&entity)
	c.Assert(err, gc.IsNil)
	c.Assert(&entity, jc.DeepEquals, expect)
}

// BaseEntityBuilder provides a convenient way to describe a
// mongodoc.BaseEntity for tests that is correctly formed and contain the
// desired information.
type BaseEntityBuilder struct {
	baseEntity *mongodoc.BaseEntity
}

// NewBaseEntity creates a new BaseEntityBuilder for the provided URL.
func NewBaseEntity(url string) BaseEntityBuilder {
	URL := charm.MustParseReference(url)
	return BaseEntityBuilder{
		baseEntity: &mongodoc.BaseEntity{
			URL:  URL,
			Name: URL.Name,
			User: URL.User,
		},
	}
}

// Promulgated sets the promulgated flag on the BaseEntity.
func (b BaseEntityBuilder) Promulgate() BaseEntityBuilder {
	b.baseEntity.Promulgated = true
	return b
}

// Build creates a mongodoc.BaseEntity from the BaseEntityBuilder.
func (b BaseEntityBuilder) Build() *mongodoc.BaseEntity {
	return b.baseEntity
}

// AssertBaseEntity checks that db contains a base entity that matches expect.
func AssertBaseEntity(c *gc.C, db *mgo.Collection, expect *mongodoc.BaseEntity) {
	var baseEntity mongodoc.BaseEntity
	err := db.FindId(expect.URL).One(&baseEntity)
	c.Assert(err, gc.IsNil)
	c.Assert(&baseEntity, jc.DeepEquals, expect)
}

func baseURL(url *charm.Reference) *charm.Reference {
	baseURL := *url
	baseURL.Series = ""
	baseURL.Revision = -1
	return &baseURL
}
