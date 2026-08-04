package persistence

import "go.mongodb.org/mongo-driver/v2/bson"

// MatchSnapshotValidator is the strict MongoDB collection validator for the
// current authoritative snapshot envelope.
func MatchSnapshotValidator() bson.M {
	return bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"_id", "schema_version", "match_version", "origin", "configuration_hash", "state", "created_at", "updated_at"},
			"properties": bson.M{
				"_id":                bson.M{"bsonType": "objectId"},
				"schema_version":     bson.M{"bsonType": "int", "enum": []int{MatchSnapshotSchemaVersion}},
				"match_version":      bson.M{"bsonType": "long", "minimum": 0},
				"origin":             bson.M{"bsonType": "string", "enum": []string{string(SnapshotOriginNative)}},
				"configuration_hash": bson.M{"bsonType": "string", "minLength": 64, "maxLength": 64},
				"state":              bson.M{"bsonType": "object"},
				"created_at":         bson.M{"bsonType": "date"},
				"updated_at":         bson.M{"bsonType": "date"},
			},
		},
	}
}
