package querytools

import (
	"api/filter/go/filtervisitor"
	"googlesql/safesql/go/safesql"
)

// The following table metadata would be from an SDL definition that looks like the following:
//
//   CREATE TABLE Singers (
//     SingerId   STRING(128) NOT NULL,
//     SingerName STRING(128) NOT NULL
//   ) PRIMARY KEY (SingerId);
//
//   CREATE TABLE Albums (
//     SingerId    STRING(128) NOT NULL,
//     AlbumId     STRING(128) NOT NULL,
//     AlbumTitle  STRING(256),
//     ReleaseDate TIMESTAMP_MICROS,
//     Rating      INT32
//   ) PRIMARY KEY (SingerId, AlbumId);
//
//   CREATE GLOBAL INDEX AlbumsByTitleReleaseDate on Albums(
//     AlbumTitle,
//     ReleaseDate
//   );
//
//   CREATE GLOBAL INDEX AlbumsByTitleAlbumIdReleaseDate on Albums(
//     AlbumTitle,
//     AlbumId,
//     ReleaseDate
//   );
//
//   -- Due to the current limitations on null filtered support in the query generator, this example
//   -- assumes that the system using this index only creates Albums where if ReleaseDate is set,
//   -- then Rating must also be set in order to use this index in the query generator.
//   CREATE GLOBAL INDEX AlbumsByTitleReleaseDateRating on Albums(
//     AlbumTitle,
//     ReleaseDate,
//     Rating
//   ) WHERE ReleaseDate IS NOT NULL AND Rating IS NOT NULL;
//
// Arbitrarily, we start base table costs at 30. We mark secondary indexes that do not have a
// STORING clause as more expensive than the base table, since they may require a JOIN to the base
// table to select fields that do not exist in the secondary index. We mark NULL FILTERED tables as
// cheaper than the base table because they may have less rows than the base table.

const (
	// SingersTableName is the name of the Singers table.
	SingersTableName = TableName("Singers")

	// SingerIDTableField is the SingerId field.
	SingerIDTableField = TableField("SingerId")
	// SingerNameTableField is the SingerName field.
	SingerNameTableField = TableField("SingerName")
	// SingerNameCountTableField is the SingerNameCount field.
	SingerNameCountTableField = TableField("SingerNameCount")

	// AlbumsTableName is the name of the Albums table.
	AlbumsTableName = TableName("Albums")
	// AlbumsByTitleReleaseDateTableName is the name of the AlbumsByTitleReleaseDate index.
	AlbumsByTitleReleaseDateTableName = TableName("AlbumsByTitleReleaseDate")
	// AlbumsByTitleAlbumIDReleaseDateTableName is the name of the AlbumsByTitleAlbumIdReleaseDate index.
	AlbumsByTitleAlbumIDReleaseDateTableName = TableName("AlbumsByTitleAlbumIdReleaseDate")
	// AlbumsByTitleReleaseDateRatingTableName is the name of the AlbumsByTitleReleaseDateRating index.
	AlbumsByTitleReleaseDateRatingTableName = TableName("AlbumsByTitleReleaseDateRating")

	// AlbumSingerIDTableField is the SingerId field.
	AlbumSingerIDTableField = TableField("SingerId")
	// AlbumIDTableField is the AlbumId field.
	AlbumIDTableField = TableField("AlbumId")
	// AlbumTitleTableField is the AlbumTitle field.
	AlbumTitleTableField = TableField("AlbumTitle")
	// ReleaseDateTableField is the ReleaseDate field.
	ReleaseDateTableField = TableField("ReleaseDate")
	// RatingTableField is the Rating field.
	RatingTableField = TableField("Rating")
)

var (
	// Singers is the TableView for the Singers table.
	Singers = TableView{
		Name:       SingersTableName,
		NameClause: safesql.New("Singers s"),
		Cost:       30,
		Fields: map[TableField]Member{
			SingerIDTableField: Member{
				Name:         SingerIDTableField,
				QueryLiteral: safesql.New("s.SingerId"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
			SingerNameTableField: Member{
				Name:                   SingerNameTableField,
				QueryLiteral:           safesql.New("s.SingerName"),
				Type:                   filtervisitor.String,
				Selectable:             true,
				DisjunctionOptimizable: true,
			},
			SingerNameCountTableField: Member{
				Name:            SingerNameCountTableField,
				QueryLiteral:    safesql.New("COUNT(s.SingerName) AS SingerNameCount"),
				Type:            filtervisitor.Integer,
				Selectable:      true,
				NeedGroupBy:     true,
				OmitFromGroupBy: true,
				Derived:         true,
			},
		},
		OrderedIndexTableFields: []TableField{SingerIDTableField},
	}

	singersRelationship = []Join{
		Join{Singers, safesql.New(` JOIN Singers s ON (a.SingerId = s.SingerId)`)},
	}

	// Albums is the TableView for the Albums table.
	Albums = TableView{
		Name:       AlbumsTableName,
		NameClause: safesql.New("Albums a"),
		Cost:       30,
		Fields: map[TableField]Member{
			AlbumSingerIDTableField: Member{
				Name:         AlbumSingerIDTableField,
				QueryLiteral: safesql.New("a.SingerId"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
			AlbumIDTableField: Member{
				Name:         AlbumIDTableField,
				QueryLiteral: safesql.New("a.AlbumId"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
			AlbumTitleTableField: Member{
				Name:         AlbumTitleTableField,
				QueryLiteral: safesql.New("a.AlbumTitle"),
				Type:         filtervisitor.String,
				Selectable:   true,
			},
			ReleaseDateTableField: Member{
				Name:         ReleaseDateTableField,
				QueryLiteral: safesql.New("a.ReleaseDate"),
				Type:         filtervisitor.Date,
				Selectable:   true,
			},
			RatingTableField: Member{
				Name:         RatingTableField,
				QueryLiteral: safesql.New("a.Rating"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
			},
		},
		Relationships:           singersRelationship,
		OrderedIndexTableFields: []TableField{SingerIDTableField, AlbumIDTableField},
	}

	// AlbumsByTitleReleaseDate is the TableView for the AlbumsByTitleReleaseDate index.
	AlbumsByTitleReleaseDate = TableView{
		Name:       AlbumsByTitleReleaseDateTableName,
		NameClause: safesql.New("Albums@{FORCE_INDEX=AlbumsByTitleReleaseDate} a"),
		Cost:       33,
		Fields: map[TableField]Member{
			AlbumSingerIDTableField: Member{
				Name:         AlbumSingerIDTableField,
				QueryLiteral: safesql.New("a.SingerId"),
				Type:         filtervisitor.String,
				Selectable:   true,
			},
			AlbumIDTableField: Member{
				Name:         AlbumIDTableField,
				QueryLiteral: safesql.New("a.AlbumId"),
				Type:         filtervisitor.String,
				Selectable:   true,
			},
			AlbumTitleTableField: Member{
				Name:         AlbumTitleTableField,
				QueryLiteral: safesql.New("a.AlbumTitle"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
			ReleaseDateTableField: Member{
				Name:         ReleaseDateTableField,
				QueryLiteral: safesql.New("a.ReleaseDate"),
				Type:         filtervisitor.Date,
				Selectable:   true,
				Indexed:      true,
			},
			RatingTableField: Member{
				Name:         RatingTableField,
				QueryLiteral: safesql.New("a.Rating"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
			},
		},
		Relationships:           singersRelationship,
		OrderedIndexTableFields: []TableField{AlbumTitleTableField, ReleaseDateTableField},
	}

	// AlbumsByTitleAlbumIDReleaseDate is the TableView for the AlbumsByTitleAlbumIdReleaseDate index.
	AlbumsByTitleAlbumIDReleaseDate = TableView{
		Name:       AlbumsByTitleAlbumIDReleaseDateTableName,
		NameClause: safesql.New("Albums@{FORCE_INDEX=AlbumsByTitleAlbumIdReleaseDate} a"),
		Cost:       34,
		Fields: map[TableField]Member{
			AlbumSingerIDTableField: Member{
				Name:         AlbumSingerIDTableField,
				QueryLiteral: safesql.New("a.SingerId"),
				Type:         filtervisitor.String,
				Selectable:   true,
			},
			AlbumIDTableField: Member{
				Name:         AlbumIDTableField,
				QueryLiteral: safesql.New("a.AlbumId"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
			AlbumTitleTableField: Member{
				Name:         AlbumTitleTableField,
				QueryLiteral: safesql.New("a.AlbumTitle"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
			ReleaseDateTableField: Member{
				Name:         ReleaseDateTableField,
				QueryLiteral: safesql.New("a.ReleaseDate"),
				Type:         filtervisitor.Date,
				Selectable:   true,
				Indexed:      true,
			},
			RatingTableField: Member{
				Name:         RatingTableField,
				QueryLiteral: safesql.New("a.Rating"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
			},
		},
		Relationships:           singersRelationship,
		OrderedIndexTableFields: []TableField{AlbumTitleTableField, AlbumIDTableField, ReleaseDateTableField},
	}

	// AlbumsByTitleAlbumIDReleaseDateOrderByOverride is the TableView for the AlbumsByTitleAlbumIdReleaseDate index with an OrderBy override.
	AlbumsByTitleAlbumIDReleaseDateOrderByOverride = TableView{
		Name:       AlbumsByTitleAlbumIDReleaseDateTableName,
		NameClause: safesql.New("Albums@{FORCE_INDEX=AlbumsByTitleAlbumIdReleaseDate} a"),
		Cost:       35,
		Fields: map[TableField]Member{
			AlbumSingerIDTableField: Member{
				Name:         AlbumSingerIDTableField,
				QueryLiteral: safesql.New("a.SingerId"),
				Type:         filtervisitor.String,
				Selectable:   true,
			},
			AlbumIDTableField: Member{
				Name:         AlbumIDTableField,
				QueryLiteral: safesql.New("a.AlbumId"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
			AlbumTitleTableField: Member{
				Name:         AlbumTitleTableField,
				QueryLiteral: safesql.New("a.AlbumTitle"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
			ReleaseDateTableField: Member{
				Name:         ReleaseDateTableField,
				QueryLiteral: safesql.New("a.ReleaseDate"),
				Type:         filtervisitor.Date,
				Selectable:   true,
				Indexed:      true,
			},
			RatingTableField: Member{
				Name:         RatingTableField,
				QueryLiteral: safesql.New("a.Rating"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
			},
		},
		Relationships:           singersRelationship,
		OrderedIndexTableFields: []TableField{AlbumTitleTableField, AlbumIDTableField, ReleaseDateTableField},
		OrderByFields:           []TableField{AlbumIDTableField},
	}

	// AlbumsByTitleReleaseDateRating is the TableView for the AlbumsByTitleReleaseDateRating index.
	AlbumsByTitleReleaseDateRating = TableView{
		Name:       AlbumsByTitleReleaseDateRatingTableName,
		NameClause: safesql.New("Albums@{FORCE_INDEX=AlbumsByTitleReleaseDateRating} a"),
		Cost:       25,
		Fields: map[TableField]Member{
			AlbumSingerIDTableField: Member{
				Name:         AlbumSingerIDTableField,
				QueryLiteral: safesql.New("a.SingerId"),
				Type:         filtervisitor.String,
				Selectable:   true,
			},
			AlbumIDTableField: Member{
				Name:         AlbumIDTableField,
				QueryLiteral: safesql.New("a.AlbumId"),
				Type:         filtervisitor.String,
				Selectable:   true,
			},
			AlbumTitleTableField: Member{
				Name:         AlbumTitleTableField,
				QueryLiteral: safesql.New("a.AlbumTitle"),
				Type:         filtervisitor.String,
				Selectable:   true,
				Indexed:      true,
			},
			ReleaseDateTableField: Member{
				Name:         ReleaseDateTableField,
				QueryLiteral: safesql.New("a.ReleaseDate"),
				Type:         filtervisitor.Date,
				Selectable:   true,
				Indexed:      true,
				NullFiltered: true,
			},
			RatingTableField: Member{
				Name:         RatingTableField,
				QueryLiteral: safesql.New("a.Rating"),
				Type:         filtervisitor.Integer,
				Selectable:   true,
				Indexed:      true,
				NullFiltered: true,
			},
		},
		Relationships:           singersRelationship,
		OrderedIndexTableFields: []TableField{AlbumTitleTableField, ReleaseDateTableField, RatingTableField},
	}

	// SingersTables is a list of TableViews for the Singers table.
	SingersTables = []TableView{Singers}
	// AlbumsTables is a list of TableViews for the Albums table.
	AlbumsTables = []TableView{Albums, AlbumsByTitleReleaseDate, AlbumsByTitleAlbumIDReleaseDate, AlbumsByTitleReleaseDateRating}
	// AllTables is a list of all TableViews.
	AllTables = []TableView{Singers, Albums, AlbumsByTitleReleaseDate, AlbumsByTitleAlbumIDReleaseDate, AlbumsByTitleReleaseDateRating}
)
